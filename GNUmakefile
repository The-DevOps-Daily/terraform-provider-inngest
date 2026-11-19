BINARY = terraform-provider-inngest

default: build

# Build the provider binary.
build:
	go build -o $(BINARY)

# Install the provider into GOBIN for manual testing.
install:
	go install

# Download Inngest's OpenAPI specs into testdata/spec/ (not committed).
spec:
	sh scripts/fetch-spec.sh

# Unit tests and the plan/apply/import tests against the in-process fake API.
# No credentials, no network. The plan/apply tests need terraform on PATH and
# skip without it.
test:
	go test ./... -count=1 -timeout 600s

# Acceptance tests against the real Inngest API. They create, change and
# destroy real resources, so they need TF_ACC=1 and INNGEST_API_KEY.
# Optional: INNGEST_ACC_ENVIRONMENT (webhook tests, default production) and
# INNGEST_ACC_APP_ID (an app synced in that environment).
testacc:
	TF_ACC=1 go test ./internal/provider/ -run '^TestAcc' -v -count=1 -timeout 30m

fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...

# Regenerate docs/ from the schema descriptions and examples/.
docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name inngest

.PHONY: default build install spec test testacc fmt vet lint docs
