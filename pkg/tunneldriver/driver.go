package tunneldriver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/go-logr/logr"
	ingressv1alpha1 "github.com/ngrok/ngrok-operator/api/ingress/v1alpha1"
	"github.com/ngrok/ngrok-operator/internal/version"
	"golang.org/x/exp/maps"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
	logrok "golang.ngrok.com/ngrok/log"
)

type k8sLogger struct {
	logger logr.Logger
}

func (l k8sLogger) Log(ctx context.Context, level logrok.LogLevel, msg string, kvs map[string]interface{}) {
	keysAndValues := []any{}
	for k, v := range kvs {
		keysAndValues = append(keysAndValues, k, v)
	}
	l.logger.V(level-4).Info(msg, keysAndValues...)
}

const (
	// TODO: Make this configurable via helm and document it so users can
	// use it for things like proxies
	customCertsPath = "/etc/ssl/certs/ngrok/"
)

// TunnelDriver is a driver for creating and deleting ngrok tunnels
type TunnelDriver struct {
	session atomic.Pointer[sessionState]
	tunnels map[string]ngrok.Tunnel
}

// TunnelDriverOpts are options for creating a new TunnelDriver
type TunnelDriverOpts struct {
	ServerAddr string
	Region     string
	RootCAs    string
	Comments   *TunnelDriverComments
}

type TunnelDriverComments struct {
	Gateway string `json:"gateway,omitempty"`
}

type sessionState struct {
	session   ngrok.Session
	readyErr  error
	healthErr error
}

// New creates and initializes a new TunnelDriver
func New(ctx context.Context, logger logr.Logger, opts TunnelDriverOpts) (*TunnelDriver, error) {
	tunnelComment := opts.Comments
	comments := []string{}

	if tunnelComment != nil {
		commentJson, err := json.Marshal(tunnelComment)
		if err != nil {
			return nil, err
		}
		commentString := string(commentJson)
		if commentString != "{}" {
			comments = append(
				comments,
				string(commentString),
			)
		}
	}
	connOpts := []ngrok.ConnectOption{
		ngrok.WithClientInfo("ngrok-operator", version.GetVersion(), comments...),
		ngrok.WithAuthtokenFromEnv(),
		ngrok.WithLogger(k8sLogger{logger}),
	}

	if opts.Region != "" {
		connOpts = append(connOpts, ngrok.WithRegion(opts.Region))
	}

	if opts.ServerAddr != "" {
		connOpts = append(connOpts, ngrok.WithServer(opts.ServerAddr))
	}

	isHostCA := opts.RootCAs == "host"

	// validate is "trusted",  "" or "host
	if !isHostCA && opts.RootCAs != "trusted" && opts.RootCAs != "" {
		return nil, fmt.Errorf("invalid value for RootCAs: %s", opts.RootCAs)
	}

	// Configure certs if the custom cert directory exists or host if set
	if _, err := os.Stat(customCertsPath); !os.IsNotExist(err) || isHostCA {
		caCerts, err := caCerts(isHostCA)
		if err != nil {
			return nil, err
		}
		connOpts = append(connOpts, ngrok.WithCA(caCerts))
	}

	if isHostCA {
		connOpts = append(connOpts, ngrok.WithTLSConfig(func(c *tls.Config) {
			c.RootCAs = nil
		}))
	}

	td := &TunnelDriver{
		tunnels: make(map[string]ngrok.Tunnel),
	}

	td.session.Store(&sessionState{
		readyErr: fmt.Errorf("attempting to connect"),
	})
	connOpts = append(connOpts,
		ngrok.WithConnectHandler(func(ctx context.Context, sess ngrok.Session) {
			td.session.Store(&sessionState{
				session: sess,
			})
		}),
		ngrok.WithDisconnectHandler(func(ctx context.Context, sess ngrok.Session, err error) {
			state := td.session.Load()

			if state.session != nil {
				// we have established session in the past, so record err only when it is going away
				if err == nil {
					td.session.Store(&sessionState{
						healthErr: fmt.Errorf("session closed"),
					})
				}
				return
			}

			if err == nil {
				// session is disconnecting, do not override error
				if state.healthErr == nil {
					td.session.Store(&sessionState{
						healthErr: fmt.Errorf("session closed"),
					})
				}
				return
			}

			if state.healthErr != nil {
				// we are already at a terminal error, just keep the first one
				return
			}

			// we didn't have a session and we are seeing disconnect error
			userErr := strings.HasPrefix(err.Error(), "authentication failed") && !strings.Contains(err.Error(), "internal server error")
			if userErr {
				// its a user error (e.g. authentication failure), so stop further
				td.session.Store(&sessionState{
					healthErr: err,
				})
				sess.Close()
			} else {
				// mark this as connecting error to return from readyz
				td.session.Store(&sessionState{
					readyErr: err,
				})
			}
		}),
	)
	//nolint:errcheck
	go ngrok.Connect(ctx, connOpts...)

	return td, nil
}

// Ready implements the healthcheck.HealthChecker interface for when the TunnelDriver is ready to serve tunnels
func (td *TunnelDriver) Ready(_ context.Context, _ *http.Request) error {
	state := td.session.Load()
	return state.readyErr
}

// Alive implements the healthcheck.HealthChecker interface for when the TunnelDriver is alive
func (td *TunnelDriver) Alive(_ context.Context, _ *http.Request) error {
	state := td.session.Load()
	return state.healthErr
}

func (td *TunnelDriver) getSession() (ngrok.Session, error) {
	state := td.session.Load()
	switch {
	case state.session != nil:
		return state.session, nil
	case state.healthErr != nil:
		return nil, state.healthErr
	case state.readyErr != nil:
		return nil, state.readyErr
	default:
		return nil, fmt.Errorf("unexpected state")
	}
}

// caCerts combines the system ca certs with a directory of custom ca certs
func caCerts(hostCA bool) (*x509.CertPool, error) {
	systemCertPool, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}

	// we're all set if we're using the host CA
	if hostCA {
		return systemCertPool, nil
	}

	// Clone the system cert pool
	customCertPool := systemCertPool.Clone()

	// Read each .crt file in the custom cert directory
	files, err := os.ReadDir(customCertsPath)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) != ".crt" {
			continue
		}

		// Read the contents of the .crt file
		certBytes, err := os.ReadFile(filepath.Join(customCertsPath, file.Name()))
		if err != nil {
			return nil, err
		}

		// Append the cert to the custom cert pool
		customCertPool.AppendCertsFromPEM(certBytes)
	}

	return customCertPool, nil
}

// CreateTunnel creates and starts a new tunnel in a goroutine. If a tunnel with the same name already exists,
// it will be stopped and replaced with a new tunnel unless the labels match.
func (td *TunnelDriver) CreateTunnel(ctx context.Context, name string, spec ingressv1alpha1.TunnelSpec) error {
	session, err := td.getSession()
	if err != nil {
		return err
	}

	log := log.FromContext(ctx)

	if tun, ok := td.tunnels[name]; ok {
		if maps.Equal(tun.Labels(), spec.Labels) {
			log.Info("Tunnel labels match existing tunnel, doing nothing")
			return nil
		}
		// There is already a tunnel with this name, start the new one and defer closing the old one
		//nolint:errcheck
		defer td.stopTunnel(context.Background(), tun)
	}

	tun, err := session.Listen(ctx, td.buildTunnelConfig(spec.Labels, spec.ForwardsTo, spec.AppProtocol))
	if err != nil {
		return err
	}
	td.tunnels[name] = tun

	protocol := ""
	if spec.BackendConfig != nil {
		protocol = spec.BackendConfig.Protocol
	}

	go handleConnections(ctx, &net.Dialer{}, tun, spec.ForwardsTo, protocol, spec.AppProtocol)
	return nil
}

// DeleteTunnel stops and deletes a tunnel
func (td *TunnelDriver) DeleteTunnel(ctx context.Context, name string) error {
	log := log.FromContext(ctx).WithValues("name", name)

	tun := td.tunnels[name]
	if tun == nil {
		log.Info("Tunnel not found while trying to delete tunnel")
		return nil
	}

	err := td.stopTunnel(ctx, tun)
	if err != nil {
		return err
	}
	delete(td.tunnels, name)
	log.Info("Tunnel deleted successfully")
	return nil
}

func (td *TunnelDriver) stopTunnel(ctx context.Context, tun ngrok.Tunnel) error {
	if tun == nil {
		return nil
	}
	return tun.CloseWithContext(ctx)
}

func (td *TunnelDriver) buildTunnelConfig(labels map[string]string, destination, appProtocol string) config.Tunnel {
	opts := []config.LabeledTunnelOption{}
	for key, value := range labels {
		opts = append(opts, config.WithLabel(key, value))
	}
	opts = append(opts, config.WithForwardsTo(destination))
	opts = append(opts, config.WithAppProtocol(appProtocol))
	return config.LabeledTunnel(opts...)
}

func handleConnections(ctx context.Context, dialer Dialer, tun ngrok.Tunnel, dest string, protocol string, appProtocol string) {
	logger := log.FromContext(ctx).WithValues("id", tun.ID(), "protocol", protocol, "dest", dest)
	for {
		conn, err := tun.Accept()
		if err != nil {
			logger.Error(err, "Error accepting connection")
			// Right now, this can only be "Tunnel closed" https://github.com/ngrok/ngrok-go/blob/e1d90c382/internal/tunnel/client/tunnel.go#L81-L89
			// Since that's terminal, that means we should give up on this loop to
			// ensure we don't leak a goroutine after a tunnel goes away.
			// Unfortunately, it's not an exported error, so we can't verify with
			// more certainty that's what's going on, but at the time of writing,
			// that should be true.
			return
		}
		connLogger := logger.WithValues("remoteAddr", conn.RemoteAddr())
		connLogger.Info("Accepted connection")

		go func() {
			ctx := log.IntoContext(ctx, connLogger)
			err := handleConn(ctx, dest, protocol, appProtocol, dialer, conn)
			if err == nil || errors.Is(err, net.ErrClosed) {
				connLogger.Info("Connection closed")
				return
			}

			connLogger.Error(err, "Error handling connection")
		}()
	}
}

func handleConn(ctx context.Context, dest string, protocol string, appProtocol string, dialer Dialer, conn net.Conn) error {
	log := log.FromContext(ctx)
	next, err := dialer.DialContext(ctx, "tcp", dest)
	if err != nil {
		return err
	}

	// Support HTTPS backends
	if protocol == "HTTPS" {
		host, _, err := net.SplitHostPort(dest)
		if err != nil {
			host = dest
		}
		var nextProtos []string
		if appProtocol == "http2" {
			nextProtos = []string{"h2", "http/1.1"}
		}

		next = tls.Client(next, &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: true,
			Renegotiation:      tls.RenegotiateFreelyAsClient,
			NextProtos:         nextProtos,
		})
	}

	var g errgroup.Group
	g.Go(func() error {
		defer func() {
			if err := next.Close(); err != nil {
				log.Info("Error closing connection to destination: %v", err)
			}
		}()

		_, err := io.Copy(next, conn)
		return err
	})
	g.Go(func() error {
		defer func() {
			if err := conn.Close(); err != nil {
				log.Info("Error closing connection from ngrok: %v", err)
			}
		}()

		_, err := io.Copy(conn, next)
		return err
	})
	return g.Wait()
}
