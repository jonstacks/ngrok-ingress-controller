.PHONY: _setup
_setup:
	../../scripts/helm-setup.sh
	helm repo add bitnami https://charts.bitnami.com/bitnami
	helm dependency build .

.PHONY: helm-lint
lint: _setup ## Lint the helm chart
	helm lint .

.PHONY: helm-test
test: _setup ## Run helm unittest plugin
	helm unittest -f 'tests/**/*_test.yaml' .

.PHONY: helm-update-snapshots
update-snapshots: _setup ## Update helm unittest snapshots
	helm unittest -f 'tests/**/*_test.yaml' --update-snapshot .
