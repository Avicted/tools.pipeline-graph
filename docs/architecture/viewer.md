# The viewer at run time

How `pipeline-graph-viewer` (`viewer/`) is structured while it runs. For what it
shows and how to use it, see [the viewer guide](../pipeline-graph-viewer.md).

Diagrams use the [legend](../architecture.md#legend); each answers the question
stated under its heading.

*Answers: what is shared, what is mutable, and what runs off the UI thread?*

The app moves through three phases:

```mermaid
stateDiagram-v2
    direction LR
    [*] --> Welcome
    Welcome --> Loading : open · drop a file · start with a path
    Loading --> Ready : Model loaded
    Loading --> Welcome : error, shown on the welcome page
    Ready --> Loading : open another file
    Ready --> Welcome : File → Close
    Ready --> Ready : reload · compare (background job, state kept)
```

Inside `Ready`, ownership is simple: one immutable model, one mutable workspace,
and threads that only ever produce new immutable values.

```mermaid
flowchart TB
    subgraph bg["background threads (ui::job)"]
        jl["load / reload<br/>Model::load"]
        jy["layout<br/>build_scene"]
        jc["compare<br/>core::diff"]
    end
    model[("Arc&lt;Model&gt; · immutable<br/>Graph · Derived · SearchIndex")]
    ws["Workspace · mutable, UI thread only<br/>screen · selection · lineage · table"]
    frame["frame loop<br/>App::update"]
    screens["screens<br/>snapshot · table · map · prod<br/>images · compare · inspector"]

    jl -- "new Model" --> model
    model -- "read (Arc clone)" --> jy
    model -- "read (Arc clone)" --> jc
    model -- "read" --> ws
    jy -- "Arc&lt;SceneLayout&gt;" --> ws
    jc -- "Diff" --> ws
    frame -- "each frame" --> ws -- "draws" --> screens

    classDef rust fill:#dcfce7,stroke:#15803d,color:#0a2e16
    classDef data fill:#fef3c7,stroke:#b45309,color:#3a2402
    class jl,jy,jc,ws,frame,screens rust
    class model data
    style bg fill:none,stroke:#15803d,stroke-dasharray:6 4
```

- `viewer/src/core` (graph, derive, search, layout, spatial, diff) doesn't depend on
  egui at all. That's what lets the unit tests, the contract test, the benchmarks and
  `ui::headless` run without a window.
- A reload builds a fresh `Workspace` around the new model, then carries over the
  selection, the screen, the table filter and the comparison.
- The budgets are checked on a synthetic graph with 48,000 nodes by `make viewer-bench`.
  It loads in about 0.3 s and stays at about 30 MB.
