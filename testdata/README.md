# API specs

The provider is built from Inngest's published OpenAPI specs:

| Spec | URL | Fetched | SHA-256 at fetch time |
| --- | --- | --- | --- |
| REST API v2 (2.0.0) | https://api-docs.inngest.com/api-specs/v2.json | 2026-11-20 | `fbe142b91b7cb85c84355855f2909b9e7371c0378d06cf96d1191f9b25f86831` |
| REST API v1 (1.0) | https://api-docs.inngest.com/api-specs/v1.json | 2026-11-20 | `7a028a01e2d5c8d33fca975c91ad12de6c8c2573b49d49686da21d2a0092c8cc` |

The specs are not copied into this repository. The v1 spec names its
license as "Inngest, Inc." and the v2 spec has no license, so the files
stay with Inngest. Run `make spec` (or `scripts/fetch-spec.sh`) to download
them into `testdata/spec/`, which git ignores.

With the specs in place, `go test ./internal/client/` also checks that every
endpoint the client calls, every JSON field of its types and the enums it
validates against still exist in the published specs. CI downloads the specs
before it runs the tests, so a change on Inngest's side fails the build.

Other public sources used:

- Authentication: https://api-docs.inngest.com/authentication
- Keys and access: https://www.inngest.com/docs/platform/api-keys
- Environments and branch deploys: https://www.inngest.com/docs-markdown/platform-and-operations/environments-and-branch-deploys
- Plan limits: https://www.inngest.com/docs-markdown/durable-execution/limits
- The v2 service definition in the open source server, for field limits
  (webhook names up to 128 characters) and the zero value of enums:
  https://github.com/inngest/inngest/blob/main/proto/api/v2/service.proto
