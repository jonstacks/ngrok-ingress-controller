/*
MIT License

Copyright (c) 2024 ngrok, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: Run "make" to regenerate code after modifying this file
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// EndpointBindingSpec defines the desired state of EndpointBinding
type EndpointBindingSpec struct {
	// Protocol is the Service protocol this Endpoint uses
	// +kubebuilder:validation:Required
	// +kubebuilder:default=`TCP`
	// +kubebuilder:validation:Enum=TCP
	Protocol string `json:"protocol"`

	// Port is the Service port this Endpoint uses
	// +kubebuilder:validation:Required
	Port int32 `json:"port"`

	// EndpointTarget is the target Service that this Endpoint projects
	// +kubebuilder:validation:Required
	Target EndpointTarget `json:"target"`
}

// EndpointBindingStatus defines the observed state of EndpointBinding
type EndpointBindingStatus struct {
	BindingEndpoint `json:",inline"`

	// HashName is the hashed output of the TargetService and TargetNamespace for unique identification
	// +kubebuilder:validation:Required
	HashedName string `json:"hashedName"`
}

// EndpointTarget hold the data for the projected Service that binds the endpoint to the k8s cluster resource
type EndpointTarget struct {
	// Service is the name of the Service that this Endpoint projects
	// +kubebuilder:validation:Required
	Service string `json:"service"`

	// Namespace is the destination Namespace for the Service this Endpoint projects
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// Port is the Service targetPort this Endpoint uses for the Pod Forwarders
	// +kubebuilder:validation:Required
	Port int32 `json:"port"`

	// Metadata is a subset of metav1.ObjectMeta that is added to the Service
	// +kube:validation:Optional
	Metadata TargetMetadata `json:"metadata,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// EndpointBinding is the Schema for the endpointbindings API
// +kubebuilder:printcolumn:name="Namespace",type="string",JSONPath=".spec.targetService"
// +kubebuilder:printcolumn:name="Service",type="string",JSONPath=".spec.targetNamespace"
// +kubebuilder:printcolumn:name="Port",type="string",JSONPath=".spec.port"
// +kubebuilder:printcolumn:name="Protocol",type="string",JSONPath=".spec.protocol"
type EndpointBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EndpointBindingSpec   `json:"spec,omitempty"`
	Status EndpointBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EndpointBindingList contains a list of EndpointBinding
type EndpointBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EndpointBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EndpointBinding{}, &EndpointBindingList{})
}
