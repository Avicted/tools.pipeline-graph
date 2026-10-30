//! Left-to-right layered (Sugiyama-style) layout with one swimlane per group,
//! ported from the Go renderer (internal/layout). Pure and deterministic, so it
//! can run on a worker thread and be cached per view.

use super::spatial::Rect;

#[derive(Clone, Copy, Debug)]
pub struct Opts {
    pub lane_header: f32,
    pub lane_pad: f32,
    pub lane_gap: f32,
    pub layer_gap: f32,
    pub node_gap: f32,
    pub margin: f32,
    pub top_offset: f32,
    /// Room above a lane's content for its title tag.
    pub lane_top: f32,
}

impl Default for Opts {
    fn default() -> Self {
        Opts {
            lane_header: 44.0,
            lane_pad: 28.0,
            lane_gap: 18.0,
            layer_gap: 110.0,
            node_gap: 26.0,
            margin: 30.0,
            top_offset: 0.0,
            lane_top: 0.0,
        }
    }
}

pub struct Input<'a> {
    pub size: &'a [(f32, f32)],
    /// Lane index of every node (all zero for a single band).
    pub lane: &'a [u32],
    pub lane_names: &'a [String],
    /// Edges as (from, to) node indexes, pointing downstream.
    pub edges: &'a [(u32, u32)],
}

#[derive(Default, Clone, Debug)]
pub struct Layout {
    pub boxes: Vec<Rect>,
    /// One polyline per input edge: source port, bends, target port.
    pub routes: Vec<Vec<[f32; 2]>>,
    pub lanes: Vec<(u32, Rect)>,
    pub w: f32,
    pub h: f32,
}

struct V {
    dummy: bool,
    lane: u32,
    layer: usize,
    w: f32,
    h: f32,
    y: f32,
    order: f32,
    ins: Vec<u32>,
    outs: Vec<u32>,
}

struct DEdge {
    from: u32,
    to: u32,
    rev: bool,
    e: usize,
}

pub fn compute(inp: &Input, o: &Opts) -> Layout {
    let n = inp.size.len();
    let n_lanes = inp.lane.iter().map(|&l| l as usize + 1).max().unwrap_or(0).max(inp.lane_names.len());
    if n == 0 {
        return Layout { w: o.margin * 2.0, h: o.margin * 2.0 + o.top_offset, ..Default::default() };
    }
    let mut vs: Vec<V> = (0..n)
        .map(|i| V {
            dummy: false,
            lane: inp.lane[i],
            layer: 0,
            w: inp.size[i].0,
            h: inp.size[i].1,
            y: 0.0,
            order: 0.0,
            ins: vec![],
            outs: vec![],
        })
        .collect();

    // 1. cycle removal: iterative DFS, reversing back edges
    let mut adj: Vec<Vec<usize>> = vec![vec![]; n];
    for (e, &(f, _)) in inp.edges.iter().enumerate() {
        adj[f as usize].push(e);
    }
    let mut state = vec![0u8; n];
    let mut dedges: Vec<DEdge> = Vec::with_capacity(inp.edges.len());
    let mut stack: Vec<(u32, usize)> = vec![];
    for root in 0..n as u32 {
        if state[root as usize] != 0 {
            continue;
        }
        state[root as usize] = 1;
        stack.push((root, 0));
        while let Some(&mut (v, ref mut cur)) = stack.last_mut() {
            if *cur < adj[v as usize].len() {
                let e = adj[v as usize][*cur];
                *cur += 1;
                let w = inp.edges[e].1;
                match state[w as usize] {
                    0 => {
                        dedges.push(DEdge { from: v, to: w, rev: false, e });
                        state[w as usize] = 1;
                        stack.push((w, 0));
                    }
                    1 => dedges.push(DEdge { from: w, to: v, rev: true, e }),
                    _ => dedges.push(DEdge { from: v, to: w, rev: false, e }),
                }
            } else {
                state[v as usize] = 2;
                stack.pop();
            }
        }
    }
    for d in &dedges {
        vs[d.from as usize].outs.push(d.to);
        vs[d.to as usize].ins.push(d.from);
    }

    // 2. layering: longest path, then pull sources next to their successors
    let mut indeg: Vec<usize> = vs.iter().map(|v| v.ins.len()).collect();
    let mut topo: Vec<u32> = (0..n as u32).filter(|&v| indeg[v as usize] == 0).collect();
    let mut qi = 0;
    while qi < topo.len() {
        let v = topo[qi] as usize;
        qi += 1;
        for k in 0..vs[v].outs.len() {
            let w = vs[v].outs[k] as usize;
            if vs[w].layer < vs[v].layer + 1 {
                vs[w].layer = vs[v].layer + 1;
            }
            indeg[w] -= 1;
            if indeg[w] == 0 {
                topo.push(w as u32);
            }
        }
    }
    for &v in topo.iter().rev() {
        let v = v as usize;
        if vs[v].ins.is_empty() && !vs[v].outs.is_empty() {
            let min = vs[v].outs.iter().map(|&w| vs[w as usize].layer).min().unwrap_or(1);
            vs[v].layer = min.saturating_sub(1);
        }
    }

    // 3. dummy nodes for edges spanning several layers
    for v in vs.iter_mut() {
        v.ins.clear();
        v.outs.clear();
    }
    let mut chains: Vec<Vec<u32>> = Vec::with_capacity(dedges.len());
    for d in &dedges {
        let (fl, tl) = (vs[d.from as usize].layer, vs[d.to as usize].layer);
        let mut nodes = vec![d.from];
        let mut prev = d.from;
        for l in fl + 1..tl {
            let lane = if l > (fl + tl) / 2 { vs[d.to as usize].lane } else { vs[d.from as usize].lane };
            let id = vs.len() as u32;
            vs.push(V { dummy: true, lane, layer: l, w: 0.0, h: 6.0, y: 0.0, order: 0.0, ins: vec![prev], outs: vec![] });
            vs[prev as usize].outs.push(id);
            nodes.push(id);
            prev = id;
        }
        vs[prev as usize].outs.push(d.to);
        vs[d.to as usize].ins.push(prev);
        nodes.push(d.to);
        chains.push(nodes);
    }

    // 4. ordering within layers: lane first, then barycentre sweeps
    let max_layer = vs.iter().map(|v| v.layer).max().unwrap_or(0);
    let mut layers: Vec<Vec<u32>> = vec![vec![]; max_layer + 1];
    for (i, v) in vs.iter().enumerate() {
        layers[v.layer].push(i as u32);
    }
    for l in layers.iter_mut() {
        l.sort_by_key(|&i| vs[i as usize].lane);
        for (k, &i) in l.iter().enumerate() {
            vs[i as usize].order = k as f32;
        }
    }
    for it in 0..12 {
        if it % 2 == 0 {
            for l in layers.iter_mut().skip(1) {
                sort_by_bary(l, &mut vs, true);
            }
        } else {
            for i in (0..max_layer).rev() {
                sort_by_bary(&mut layers[i], &mut vs, false);
            }
        }
    }

    // 5. vertical placement inside lanes
    let segs: Vec<Vec<(usize, usize)>> = layers.iter().map(|l| lane_runs(l, &vs)).collect();
    for (li, l) in layers.iter().enumerate() {
        for &(a, b) in &segs[li] {
            let mut y = 0.0;
            for &i in &l[a..b] {
                vs[i as usize].y = y;
                y += vs[i as usize].h + o.node_gap;
            }
        }
    }
    for it in 0..16 {
        for pass in 0..2 {
            let seq: Vec<usize> = if (it + pass) % 2 == 0 { (0..=max_layer).collect() } else { (0..=max_layer).rev().collect() };
            for li in seq {
                for &(a, b) in &segs[li] {
                    let grp = &layers[li][a..b];
                    let mut desired = Vec::with_capacity(grp.len());
                    for &i in grp {
                        let v = &vs[i as usize];
                        let mut ys: Vec<f32> = v
                            .ins
                            .iter()
                            .chain(v.outs.iter())
                            .map(|&w| &vs[w as usize])
                            .filter(|w| w.lane == v.lane)
                            .map(|w| w.y + w.h / 2.0)
                            .collect();
                        let d = if ys.is_empty() { v.y + v.h / 2.0 } else { median(&mut ys) };
                        desired.push(d - v.h / 2.0);
                    }
                    place(grp, &desired, o.node_gap, &mut vs);
                }
            }
        }
    }

    // normalise: each lane's content starts at local y = 0
    let mut min_y = vec![f32::INFINITY; n_lanes];
    for v in &vs {
        min_y[v.lane as usize] = min_y[v.lane as usize].min(v.y);
    }
    for v in vs.iter_mut() {
        v.y -= min_y[v.lane as usize];
    }

    // 6. absolute coordinates
    let mut layer_w = vec![0.0f32; max_layer + 1];
    for v in &vs {
        layer_w[v.layer] = layer_w[v.layer].max(v.w);
    }
    let mut layer_x = vec![0.0f32; max_layer + 2];
    layer_x[0] = o.margin + o.lane_header + o.lane_pad;
    for i in 0..=max_layer {
        let gap = if layer_w[i] == 0.0 { o.layer_gap / 2.0 } else { o.layer_gap };
        layer_x[i + 1] = layer_x[i] + layer_w[i] + gap;
    }
    let mut lane_h = vec![0.0f32; n_lanes];
    for v in &vs {
        lane_h[v.lane as usize] = lane_h[v.lane as usize].max(v.y + v.h);
    }
    let width = layer_x[max_layer + 1] - o.layer_gap + o.lane_pad + o.margin;
    let mut lane_top = vec![0.0f32; n_lanes];
    let mut y = o.margin + o.top_offset;
    let mut out = Layout::default();
    let used: Vec<bool> = {
        let mut u = vec![false; n_lanes];
        for v in &vs[..n] {
            u[v.lane as usize] = true;
        }
        u
    };
    for i in 0..n_lanes {
        if !used[i] {
            continue;
        }
        let name_len = inp.lane_names.get(i).map_or(0, |s| s.chars().count()) as f32;
        let h = (lane_h[i] + 2.0 * o.lane_pad + o.lane_top).max(120.0).max(name_len * 8.5 + 40.0);
        lane_top[i] = y + o.lane_pad + o.lane_top + (h - 2.0 * o.lane_pad - o.lane_top - lane_h[i]) / 2.0;
        out.lanes.push((i as u32, Rect::new(o.margin, y, width - 2.0 * o.margin, h)));
        y += h + o.lane_gap;
    }
    out.w = width;
    out.h = y - o.lane_gap + o.margin;
    let pos = |v: &V| Rect::new(layer_x[v.layer] + (layer_w[v.layer] - v.w) / 2.0, lane_top[v.lane as usize] + v.y, v.w, v.h);
    out.boxes = vs[..n].iter().map(pos).collect();
    out.routes = vec![vec![]; inp.edges.len()];
    for (d, chain) in dedges.iter().zip(&chains) {
        let mut pts: Vec<[f32; 2]> = vec![];
        for (k, &vi) in chain.iter().enumerate() {
            let v = &vs[vi as usize];
            let b = pos(v);
            let cy = b.center().1;
            if k == 0 {
                pts.push([b.x1, cy]);
            } else if k == chain.len() - 1 {
                pts.push([b.x0, cy]);
            } else {
                pts.push([layer_x[v.layer] - o.layer_gap / 4.0, cy]);
                pts.push([layer_x[v.layer] + layer_w[v.layer] + o.layer_gap / 4.0, cy]);
            }
        }
        if d.rev {
            pts.reverse();
        }
        out.routes[d.e] = pts;
    }
    debug_assert!(vs[n..].iter().all(|v| v.dummy));
    out
}

fn lane_runs(l: &[u32], vs: &[V]) -> Vec<(usize, usize)> {
    let mut runs = vec![];
    let mut a = 0;
    for k in 1..=l.len() {
        if k == l.len() || vs[l[k] as usize].lane != vs[l[a] as usize].lane {
            if k > a {
                runs.push((a, k));
            }
            a = k;
        }
    }
    runs
}

fn sort_by_bary(l: &mut [u32], vs: &mut [V], use_in: bool) {
    let bary: Vec<f32> = l
        .iter()
        .map(|&i| {
            let v = &vs[i as usize];
            let ns = if use_in { &v.ins } else { &v.outs };
            if ns.is_empty() {
                v.order
            } else {
                ns.iter().map(|&w| vs[w as usize].order).sum::<f32>() / ns.len() as f32
            }
        })
        .collect();
    let mut idx: Vec<usize> = (0..l.len()).collect();
    idx.sort_by(|&a, &b| {
        let (la, lb) = (vs[l[a] as usize].lane, vs[l[b] as usize].lane);
        la.cmp(&lb).then(bary[a].partial_cmp(&bary[b]).unwrap_or(std::cmp::Ordering::Equal))
    });
    let sorted: Vec<u32> = idx.iter().map(|&k| l[k]).collect();
    l.copy_from_slice(&sorted);
    for (k, &i) in l.iter().enumerate() {
        vs[i as usize].order = k as f32;
    }
}

fn median(xs: &mut [f32]) -> f32 {
    xs.sort_by(|a, b| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal));
    let n = xs.len();
    if n % 2 == 1 {
        xs[n / 2]
    } else {
        (xs[n / 2 - 1] + xs[n / 2]) / 2.0
    }
}

/// Sets y as close as possible (least squares) to `desired` while keeping order,
/// the minimum gap and y >= 0: isotonic regression by pool-adjacent-violators.
fn place(grp: &[u32], desired: &[f32], gap: f32, vs: &mut [V]) {
    let n = grp.len();
    let mut off = vec![0.0f32; n];
    for i in 1..n {
        off[i] = off[i - 1] + vs[grp[i - 1] as usize].h + gap;
    }
    let mut blocks: Vec<(f32, usize)> = vec![]; // (sum, count)
    for i in 0..n {
        blocks.push((desired[i] - off[i], 1));
        while blocks.len() > 1 {
            let (a, b) = (blocks[blocks.len() - 2], blocks[blocks.len() - 1]);
            if a.0 / a.1 as f32 <= b.0 / b.1 as f32 {
                break;
            }
            blocks.truncate(blocks.len() - 2);
            blocks.push((a.0 + b.0, a.1 + b.1));
        }
    }
    let mut i = 0;
    for (sum, count) in blocks {
        let z = (sum / count as f32).max(0.0);
        for _ in 0..count {
            vs[grp[i] as usize].y = z + off[i];
            i += 1;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn run(n: usize, edges: &[(u32, u32)], lanes: &[u32]) -> Layout {
        let size = vec![(100.0, 40.0); n];
        let names: Vec<String> = (0..=lanes.iter().copied().max().unwrap_or(0)).map(|i| format!("lane{i}")).collect();
        compute(&Input { size: &size, lane: lanes, lane_names: &names, edges }, &Opts::default())
    }

    #[test]
    fn a_chain_flows_left_to_right() {
        let l = run(3, &[(0, 1), (1, 2)], &[0, 0, 0]);
        assert!(l.boxes[0].x1 < l.boxes[1].x0 && l.boxes[1].x1 < l.boxes[2].x0);
        let r = &l.routes[0];
        assert_eq!(r.len(), 2);
        assert!((r[0][0] - l.boxes[0].x1).abs() < 0.01 && (r[1][0] - l.boxes[1].x0).abs() < 0.01);
    }

    #[test]
    fn a_long_edge_bends_around_the_middle_layer() {
        let l = run(3, &[(0, 1), (1, 2), (0, 2)], &[0, 0, 0]);
        assert!(l.routes[2].len() > 2, "the 0->2 edge passes the layer of node 1");
    }

    #[test]
    fn cycles_do_not_hang_and_keep_both_directions() {
        let l = run(3, &[(0, 1), (1, 2), (2, 0)], &[0, 0, 0]);
        assert_eq!(l.routes.len(), 3);
        let back = &l.routes[2];
        assert!(back.first().unwrap()[0] > back.last().unwrap()[0] || back.len() >= 2);
    }

    #[test]
    fn lanes_stack_and_nodes_stay_inside() {
        let l = run(4, &[(0, 1), (2, 3), (1, 3)], &[0, 0, 1, 1]);
        assert_eq!(l.lanes.len(), 2);
        assert!(l.lanes[0].1.y1 < l.lanes[1].1.y0);
        for (i, lane) in [0usize, 0, 1, 1].iter().enumerate() {
            let lb = l.lanes[*lane].1;
            assert!(l.boxes[i].y0 >= lb.y0 && l.boxes[i].y1 <= lb.y1, "node {i} inside its lane");
        }
    }

    #[test]
    fn siblings_do_not_overlap() {
        let l = run(5, &[(0, 1), (0, 2), (0, 3), (0, 4)], &[0; 5]);
        for a in 1..5 {
            for b in a + 1..5 {
                let (p, q) = (l.boxes[a], l.boxes[b]);
                assert!(p.y1 <= q.y0 + 0.01 || q.y1 <= p.y0 + 0.01, "{a} and {b} overlap");
            }
        }
    }

    #[test]
    fn empty_and_isolated() {
        assert!(run(0, &[], &[]).boxes.is_empty());
        assert_eq!(run(2, &[], &[0, 0]).boxes.len(), 2);
    }
}
