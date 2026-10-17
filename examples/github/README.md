# Example: Globex (GitHub Actions)

Four fictional repositories whose pipelines are GitHub Actions workflows.

```sh
make example-github   # from the repository root; writes out/github/
```

| Repo | What it shows |
|------|---------------|
| `base-images` | `docker/build-push-action` pushing `base-go`, weekly `schedule`, a `make scan` step |
| `shared-workflows` | a reusable workflow (`workflow_call`) with chained jobs |
| `api-service` | `needs:` chains, a job that `uses:` the reusable workflow from another repo, a `container:` image, `upload-artifact`, an image built `FROM` the base image, and a `workflow_run` deploy with `make` |
| `web-app` | `upload-artifact` / `download-artifact` becoming an artifact dependency, `make image` building and pushing an image, a `workflow_run` publish |

The run reports no warnings.
