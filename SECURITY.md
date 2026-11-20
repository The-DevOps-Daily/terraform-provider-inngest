# Security policy

## Supported versions

Security fixes target the latest minor release series. Use its latest patch
release. Older minor series are not supported. Until the first release, report
issues against the current `main` branch.

## Report a vulnerability

Report security issues privately through GitHub's
[Report a vulnerability form](https://github.com/The-DevOps-Daily/terraform-provider-inngest/security/advisories/new).
This uses private security advisories. Do not open a public issue for a
vulnerability.

Include:

- The provider and Terraform versions, and your operating system.
- A description of the issue and its possible impact.
- Steps to reproduce it, with a small configuration if useful.
- Relevant logs or error messages with secrets removed.
- Any suggested fix or workaround.

The DevOps Daily maintainers aim to acknowledge reports within 3 working days.
We will use the private advisory to discuss findings, a fix and disclosure.

## Scope and credentials

This policy covers this Terraform provider and its repository. Issues in the
Inngest service should be reported through Inngest's own security channels.

Never paste API keys or signing keys in issues. Remove credentials, webhook
URLs and other sensitive data from configurations, logs and state before
sharing them, including in private reports. If a key has been exposed, revoke
or rotate it through the Inngest dashboard.

This project is maintained by [DevOps Daily](https://devops-daily.com).
