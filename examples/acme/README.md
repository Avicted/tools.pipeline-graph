# Example: Acme

Seven fictional repositories with TeamCity Kotlin DSL, Makefiles and scripts
that link together the way a real organisation's pipelines do.

```sh
make example        # from the repository root; writes out/acme/
```

| Repo | What it shows |
|------|---------------|
| `platform-images` | `Template` with shared trigger/params/deps, base images built by make targets, a nightly scheduled scan |
| `shared-libs` | step calling `./ci/verify.sh` (followed into make calls), `make -C go`, gradle step, cross-repo dependency on a base image |
| `infra-terraform` | helper functions `plan(env)` / `apply(env)` chained with `sequential`, `RelativeId` artifact deps |
| `orders-service` | Makefile `include`s, recursive `$(MAKE) -C deploy`, Dockerfile `FROM ${BASE}`, `deployTo(env)` helper with conditional `finishBuildTrigger`, cross-repo artifact + infra dependencies |
| `payments-service` | builds defined via a custom `ServiceBuild` base class, extension helper `Dependencies.needsKotlinSdk()`, `dockerCommand` runner |
| `web-frontend` | `exec` runner calling make, E2E consuming the orders/payments images, finish-build trigger to CDN publish |
| `release-train` | weekly schedule, `sequential`/`parallel` composite chaining deploys across all repos, one reference to a repo that is not analysed (shown in the *External* lane) |

The run reports one warning by design: `Legacy_Billing_DeployProd` lives in a
repository that isn't part of the example.
