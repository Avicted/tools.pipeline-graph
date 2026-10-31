# Security: context and trust boundaries

Where untrusted input enters pipeline-graph, and what it can touch.

Diagrams use the [legend](../architecture.md#legend); each answers the question
stated under its heading.

*Answers: where does untrusted input enter, and what can it touch?*

```mermaid
%% view: security
flowchart LR
    users(["pipeline designers · tech leads · QA · DevSecOps"])
    subgraph untrusted["untrusted"]
        repos[("analysed repositories<br/>.teamcity · .github/workflows<br/>Makefiles · Dockerfiles")]
    end
    subgraph host["your machine"]
        subgraph box["Docker container · runs as your uid"]
            cli["pipeline-graph<br/>parses only, runs nothing<br/>from the repositories"]
        end
        creds["ssh-agent socket<br/>known_hosts · /etc/passwd (ro)"]
        cache[("~/.cache/pipeline-graph<br/>shallow clones (rw)")]
        out[("project dir → /work (rw)<br/>config in · out/ written")]
        viewer["pipeline-graph-viewer"]
    end
    repos -- "git clone --depth 1" --> cli
    creds -. "auth for git only" .-> cli
    cli <--> cache
    cli -- "drawio · vsdx · html · mmd · dot · graph.json" --> out
    out -- "graph.json" --> viewer
    users --> viewer
    users -- "browser · draw.io · Miro" --> out

    classDef untrusted fill:#fde2e1,stroke:#c0392b,color:#3b0d0a,stroke-width:2px
    classDef go fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    classDef data fill:#fef3c7,stroke:#b45309,color:#3a2402
    classDef rust fill:#dcfce7,stroke:#15803d,color:#0a2e16
    classDef actor fill:#ffffff,stroke:#111827,color:#111827
    class repos untrusted
    class cli go
    class creds,cache,out data
    class viewer rust
    class users actor
    style untrusted fill:none,stroke:#c0392b,stroke-dasharray:6 4
    style host fill:none,stroke:#6b7280,stroke-dasharray:6 4
    style box fill:none,stroke:#1d4ed8,stroke-dasharray:6 4
```

- Repository content is **parsed, never executed**. The only programs the CLI starts are
  `git`, in `internal/fetch`, and Graphviz `dot`, on its own `pipelines.dot`.
- git is the only program that touches the network. It may use only http, https, ssh, git and
  file URLs (`GIT_ALLOW_PROTOCOL`), the clone cache is private to the user (mode 0700), and a
  failed clone never deletes a directory the run did not create.
- Repository files are read only through `internal/repofs`: a path that leaves the
  repository (`..`, an absolute path, a symlink out), anything that is not a regular file
  (fifo, device) and any file over 4 MiB is refused with a warning, so a hostile
  `include ../../.env` or a symlink to a secret never reaches `graph.json`.
- The Kotlin parser stops nesting at `kts.MaxDepth` (3000) and reports the file with a warning:
  Go cannot recover from a stack overflow, so a file nested millions of levels deep would
  otherwise kill the whole run.
- Text from the repositories ends up in `.drawio` and `.vsdx` files. It is escaped and stripped
  of characters XML 1.0 forbids (`render.xmlEscape`), so a NUL in a job name cannot make
  draw.io or Visio reject the file.
- The container sees only what `make run` mounts: the project directory, the clone
  cache, and, read-only, what git needs to authenticate.
- Its only network traffic is git. `-serve` is a local file server for `out/`, mounted
  read-only (`make serve`).
