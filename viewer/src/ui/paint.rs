//! Drawing the map: lanes, edges, cards and the legend. These only read a scene layout and a
//! few facts about what is highlighted, and draw; no state of the application changes here.

use super::theme::{self, Palette};
use super::workspace::{Lineage, LineageMode};
use crate::core::derive::{Class, ENTRY_MANUAL, ENTRY_SCHEDULE, ENTRY_VCS};
use crate::core::diff::{Change, Diff};
use crate::core::graph::Kind;
use crate::core::model::Model;
use crate::core::scene::{SEdge, SceneLayout};
use crate::core::spatial::Rect;
use egui::epaint::CubicBezierShape;
use egui::text::{LayoutJob, TextFormat, TextWrapping};
use egui::{Align2, Color32, FontId, Painter, Pos2, Shape, Stroke, Vec2};
use std::sync::Arc;

/// What the selected job lights up: the lineage and the mode that says which side of it counts.
pub(super) struct Highlight<'a> {
    pub(super) lin: Option<&'a Lineage>,
    mode: LineageMode,
    /// Nodes outside the lineage are drawn faint (not on the Compare screen, where marks matter).
    pub(super) dim_on: bool,
    /// Nodes outside the lineage are left out altogether.
    pub(super) hide_others: bool,
}

impl<'a> Highlight<'a> {
    pub(super) fn new(lin: Option<&'a Lineage>, mode: LineageMode, dim: bool) -> Highlight<'a> {
        Highlight {
            lin,
            mode,
            dim_on: lin.is_some() && dim,
            hide_others: lin.is_some() && (mode != LineageMode::Off || lin.is_some_and(|l| l.route.is_some())),
        }
    }

    /// Whether node `gid` is part of what is shown, given the mode.
    pub(super) fn allowed(&self, gid: u32) -> bool {
        let Some(l) = self.lin else { return true };
        if let Some(r) = &l.route {
            return r.set.get(gid);
        }
        match self.mode {
            LineageMode::Off => true,
            LineageMode::Both => l.up.get(gid) || l.down.get(gid),
            LineageMode::Down => l.down.get(gid),
            LineageMode::Up => l.up.get(gid),
        }
    }
}

pub(super) fn draw_lanes(
    painter: &Painter,
    lay: &SceneLayout,
    pal: &Palette,
    view: &Rect,
    rect: egui::Rect,
    k: f32,
    to_screen: &impl Fn([f32; 2]) -> Pos2,
) {
    // lanes
    let lane_font = FontId::proportional((12.0 * k.max(0.6)).clamp(11.0, 15.0));
    for (i, (name, r)) in lay.lanes.iter().enumerate() {
        if !r.intersects(view) {
            continue;
        }
        let sr = egui::Rect::from_min_max(to_screen([r.x0, r.y0]), to_screen([r.x1, r.y1]));
        painter.rect_filled(sr, 8, if i % 2 == 0 { pal.lane } else { pal.lane_alt });
        painter.rect_stroke(sr, 8, super::theme::stroke(1.0, pal.line), egui::StrokeKind::Inside);
        // the tag stays in view while its lane is scrolled
        let x = (sr.min.x + 8.0).max(rect.min.x + 8.0).min(sr.max.x - 60.0);
        let galley = painter.layout_no_wrap(name.clone(), lane_font.clone(), Color32::WHITE);
        let tag = egui::Rect::from_min_size(Pos2::new(x, sr.min.y + 6.0), galley.size() + Vec2::new(14.0, 6.0));
        let col = if name == "External" { pal.warn } else { Color32::from_rgb(0x47, 0x4F, 0x5E) };
        painter.rect_filled(tag, 5, col);
        painter.galley(tag.min + Vec2::new(7.0, 3.0), galley, Color32::WHITE);
    }
}

/// Draws the visible edges and returns the one under the pointer, if any.
#[allow(clippy::too_many_arguments)]
pub(super) fn draw_edges(
    painter: &Painter,
    lay: &SceneLayout,
    pal: &Palette,
    k: f32,
    to_screen: &impl Fn([f32; 2]) -> Pos2,
    vis_e: &[u32],
    hl: &Highlight,
    hover_node: Option<u32>,
    hover_pos: Option<Pos2>,
) -> Option<u32> {
    // edges
    let hit_dist = 6.0f32;
    let mut hover_edge: Option<u32> = None;
    for &ei in vis_e {
        let e = &lay.edges[ei as usize];
        let (ga, gb) = (lay.nodes[e.a as usize].gid, lay.nodes[e.b as usize].gid);
        if !lay.aggregated && hl.hide_others && !(hl.allowed(ga) && hl.allowed(gb)) {
            continue;
        }
        let mut st = theme::edge_style(e.kind, pal.dark);
        let mut color = st.color;
        let mut width = st.width * k.clamp(0.7, 1.2);
        let mut lit = false;
        if lay.aggregated {
            color = pal.muted;
            width = (1.5 + (e.weight as f32).ln_1p()).min(7.0);
            st.dash = None;
        }
        if let Some(l) = hl.lin {
            if !lay.aggregated {
                let on_route = l.route.as_ref().map(|r| r.set.get(ga) && r.set.get(gb));
                let up = l.up.get(ga) && l.up.get(gb);
                let down = l.down.get(ga) && l.down.get(gb);
                match on_route {
                    Some(true) => {
                        lit = true;
                        color = pal.accent;
                    }
                    Some(false) => {}
                    None if up => {
                        lit = true;
                        color = pal.up;
                    }
                    None if down => {
                        lit = true;
                        color = pal.down;
                    }
                    None => {}
                }
                if lit {
                    width += 1.0;
                } else if hl.dim_on {
                    color = theme::with_alpha(color, 36);
                }
            }
        }
        if hover_node.is_none() && !lay.aggregated {
            if let Some(p) = hover_pos {
                if near_polyline(&e.pts, p, to_screen, hit_dist) {
                    hover_edge = Some(ei);
                    width += 0.8;
                }
            }
        }
        draw_edge(painter, e, to_screen, color, width, st.dash.filter(|_| k > 0.2), k);
        if lay.aggregated && k > 0.25 {
            let a = to_screen(e.pts[0]);
            let b = to_screen(*e.pts.last().unwrap());
            let c = Pos2::new((a.x + b.x) / 2.0, (a.y + b.y) / 2.0);
            let gal = painter.layout_no_wrap(e.weight.to_string(), FontId::proportional(12.0), pal.text);
            let r = egui::Rect::from_center_size(c, gal.size() + Vec2::new(10.0, 4.0));
            painter.rect_filled(r, 8, pal.card);
            painter.rect_stroke(r, 8, super::theme::stroke(1.0, pal.line), egui::StrokeKind::Inside);
            painter.galley(r.min + Vec2::new(5.0, 2.0), gal, pal.text);
        }
    }
    hover_edge
}

#[allow(clippy::too_many_arguments)]
pub(super) fn draw_nodes(
    painter: &Painter,
    lay: &SceneLayout,
    m: &Model,
    pal: &Palette,
    k: f32,
    to_screen: &impl Fn([f32; 2]) -> Pos2,
    vis_n: &[u32],
    hl: &Highlight,
    sel: Option<u32>,
    hover_node: Option<u32>,
    diff: Option<&Diff>,
) {
    // nodes
    for &i in vis_n {
        let n = &lay.nodes[i as usize];
        let sr = egui::Rect::from_min_max(to_screen([n.rect.x0, n.rect.y0]), to_screen([n.rect.x1, n.rect.y1]));
        if lay.aggregated {
            draw_repo_card(painter, sr, &lay.agg_text[i as usize], pal, hover_node == Some(i), k);
            continue;
        }
        let v = n.gid;
        if hl.hide_others && !hl.allowed(v) {
            continue;
        }
        let related = hl.lin.map(|l| (l.root == v, l.up.get(v), l.down.get(v), l.route.as_ref().is_some_and(|r| r.set.get(v))));
        let change = diff.map(|df| df.state[v as usize]).unwrap_or(Change::Same);
        let vis = CardVis {
            selected: sel == Some(v),
            hovered: hover_node == Some(i),
            up: related.is_some_and(|r| r.1 && !r.0),
            down: related.is_some_and(|r| r.2 && !r.0),
            on_route: related.is_some_and(|r| r.3 && !r.0),
            dim: hl.dim_on && related.is_some_and(|r| !r.0 && !r.1 && !r.2 && !r.3),
            change,
        };
        draw_card(painter, sr, v, m, pal, &vis, k);
    }
}

pub(super) fn draw_legend(painter: &Painter, lay: &SceneLayout, pal: &Palette, rect: egui::Rect) {
    // legend
    if !lay.aggregated && !lay.kinds.is_empty() {
        let mut y = rect.max.y - 14.0;
        let mut kinds = lay.kinds.clone();
        kinds.sort();
        for kd in kinds.iter().rev() {
            let st = theme::edge_style(*kd, pal.dark);
            let (x0, x1) = (rect.min.x + 14.0, rect.min.x + 44.0);
            match st.dash {
                Some((dl, gap)) => {
                    painter.extend(Shape::dashed_line(
                        &[Pos2::new(x0, y), Pos2::new(x1, y)],
                        super::theme::stroke(st.width.max(1.5), st.color),
                        dl.min(5.0),
                        gap.min(3.0),
                    ));
                }
                None => {
                    painter.line_segment([Pos2::new(x0, y), Pos2::new(x1, y)], super::theme::stroke(st.width.max(1.5), st.color));
                }
            }
            painter.text(Pos2::new(x1 + 8.0, y), Align2::LEFT_CENTER, kd.name(), FontId::proportional(11.5), pal.muted);
            y -= 17.0;
        }
    }
}

struct CardVis {
    selected: bool,
    hovered: bool,
    up: bool,
    down: bool,
    on_route: bool,
    dim: bool,
    change: Change,
}

fn fit_text(painter: &Painter, text: &str, font: FontId, color: Color32, max_w: f32) -> Arc<egui::Galley> {
    let mut job = LayoutJob::single_section(text.to_owned(), TextFormat::simple(font, color));
    job.wrap = TextWrapping { max_width: max_w.max(8.0), max_rows: 1, break_anywhere: true, overflow_character: Some('…') };
    painter.layout_job(job)
}

fn pill(painter: &Painter, left_top: Pos2, text: &str, fg: Color32, bg: Color32) -> f32 {
    let galley = painter.layout_no_wrap(text.to_owned(), FontId::proportional(10.5), fg);
    let r = egui::Rect::from_min_size(left_top, galley.size() + Vec2::new(10.0, 3.0));
    painter.rect_filled(r, 4, bg);
    painter.galley(r.min + Vec2::new(5.0, 1.5), galley, fg);
    r.width() + 4.0
}

fn draw_card(painter: &Painter, r: egui::Rect, v: u32, m: &Model, pal: &Palette, vis: &CardVis, k: f32) {
    let (g, d) = (&m.g, &m.d);
    let dim = |c: Color32| if vis.dim { c.gamma_multiply(0.3) } else { c };
    let kind = g.kind[v as usize];
    let accent = theme::kind_color(kind, g.composite[v as usize], pal.dark);
    let fill = if vis.selected { pal.card_sel } else { pal.card };
    let text = if vis.selected { pal.card_sel_text } else { pal.text };
    let muted = if vis.selected { theme::with_alpha(pal.card_sel_text, 170) } else { pal.muted };
    let corner = (7.0 * k.clamp(0.4, 1.0)).round() as u8;
    // zoomed out, a card grows sideways (into the gap between columns) so its title stays readable
    let titles_only = (0.2..0.55).contains(&k);
    let mut title_gal = None;
    let mut r = r;
    if titles_only {
        let size = (14.0 * k).clamp(11.0, 15.0);
        let reach = r.width() + 0.9 * 64.0 * k;
        let gal = fit_text(painter, g.node_label(v), FontId::proportional(size), dim(text), reach - 18.0);
        let want = gal.size().x + 20.0;
        if want > r.width() {
            r = egui::Rect::from_center_size(r.center(), Vec2::new(want.min(reach), r.height()));
        }
        title_gal = Some(gal);
    }
    painter.rect_filled(r, corner, dim(fill));
    let mut stroke = super::theme::stroke(if vis.hovered { 1.8 } else { 1.0 }, if vis.hovered { accent } else { pal.line });
    let ring = if vis.on_route {
        Some(pal.accent)
    } else if vis.up {
        Some(pal.up)
    } else if vis.down {
        Some(pal.down)
    } else {
        None
    };
    if let Some(c) = ring {
        stroke = super::theme::stroke(2.2, c);
    }
    match vis.change {
        Change::Added => stroke = super::theme::stroke(2.6, pal.added),
        Change::Changed => stroke = super::theme::stroke(2.6, pal.changed),
        Change::Same => {}
    }
    painter.rect_stroke(r, corner, super::theme::stroke(stroke.width, dim(stroke.color)), egui::StrokeKind::Inside);
    if k < 0.2 {
        // far away: a coloured block is all that is legible
        painter.rect_filled(egui::Rect::from_min_size(r.min, Vec2::new(r.width().min(5.0), r.height())), 2, dim(accent));
        return;
    }
    let pad = 9.0 * k.clamp(0.7, 1.0);
    let bar = egui::Rect::from_min_size(r.min, Vec2::new(4.0 * k.clamp(0.6, 1.0), r.height()));
    painter.rect_filled(bar, corner, dim(accent));
    let clip = painter.with_clip_rect(r.shrink(1.0));
    let inner_w = r.width() - 2.0 * pad - 4.0;
    let label = g.node_label(v);
    if let Some(gal) = title_gal {
        let y = r.center().y - gal.size().y / 2.0;
        clip.galley(Pos2::new(r.min.x + pad + 4.0, y), gal, text);
        return;
    }
    let tsize = (14.0 * k).clamp(11.0, 17.0);
    let title = if kind == Kind::Make { format!("make {label}") } else { label.to_string() };
    let gal = fit_text(&clip, &title, FontId::proportional(tsize), dim(text), inner_w - 30.0);
    let mut y = r.min.y + 7.0 * k.clamp(0.7, 1.2);
    let th = gal.size().y;
    clip.galley(Pos2::new(r.min.x + pad + 4.0, y), gal, text);
    let down = d.down[v as usize];
    if k >= 0.75 && down > 0 && kind != Kind::Make {
        let c = if vis.selected { pal.card_sel_text } else { pal.down };
        clip.text(
            Pos2::new(r.max.x - pad, y + th / 2.0),
            Align2::RIGHT_CENTER,
            format!("↓{down}"),
            FontId::proportional(11.0 * k.clamp(0.8, 1.2)),
            dim(c),
        );
    }
    y += th + 1.0;
    let sub = if kind == Kind::Build && !g.s(g.project[v as usize]).is_empty() {
        format!("{} · {}", g.s(g.project[v as usize]), g.s(g.sub[v as usize]))
    } else {
        g.s(g.sub[v as usize]).to_string()
    };
    let ssize = (11.0 * k).clamp(9.5, 13.0);
    let gal = fit_text(&clip, &sub, FontId::proportional(ssize), dim(muted), inner_w);
    let sh = gal.size().y;
    clip.galley(Pos2::new(r.min.x + pad + 4.0, y), gal, muted);
    y += sh + 4.0 * k.clamp(0.8, 1.2);
    if k >= 0.8 && r.max.y - y > 12.0 {
        let mut x = r.min.x + pad + 4.0;
        let class = d.class[v as usize];
        if class != Class::None {
            x += pill(&clip, Pos2::new(x, y), class.name(), Color32::WHITE, dim(theme::class_color(class, pal.dark)));
        }
        let e = d.entry[v as usize];
        let ink = if vis.selected { pal.card_sel_text } else { pal.muted };
        let chip_bg = if vis.selected { theme::with_alpha(pal.card_sel_text, 40) } else { theme::with_alpha(pal.muted, 40) };
        for (bit, name) in [(ENTRY_VCS, "commit"), (ENTRY_SCHEDULE, "scheduled"), (ENTRY_MANUAL, "manual")] {
            if e & bit != 0 {
                x += pill(&clip, Pos2::new(x, y), name, dim(ink), chip_bg);
            }
        }
        if kind == Kind::Image {
            x += pill(&clip, Pos2::new(x, y), "image", dim(ink), chip_bg);
        }
        if d.no_test_up[v as usize] {
            pill(&clip, Pos2::new(x, y), "no test upstream", dim(pal.card), pal.changed);
        }
    }
    if vis.change != Change::Same {
        let c = if vis.change == Change::Added { pal.added } else { pal.changed };
        painter.circle_filled(r.right_top() + Vec2::new(-4.0, 4.0), 5.0, c);
    }
}

fn draw_repo_card(painter: &Painter, r: egui::Rect, text: &(String, String), pal: &Palette, hovered: bool, k: f32) {
    painter.rect_filled(r, 10, pal.card);
    painter.rect_stroke(
        r,
        10,
        super::theme::stroke(if hovered { 2.0 } else { 1.0 }, if hovered { pal.accent } else { pal.line }),
        egui::StrokeKind::Inside,
    );
    let clip = painter.with_clip_rect(r.shrink(1.0));
    let tsize = (17.0 * k).clamp(12.0, 22.0);
    clip.text(r.min + Vec2::new(14.0, r.height() * 0.32), Align2::LEFT_CENTER, &text.0, FontId::proportional(tsize), pal.text);
    if k > 0.35 {
        clip.text(
            r.min + Vec2::new(14.0, r.height() * 0.70),
            Align2::LEFT_CENTER,
            &text.1,
            FontId::proportional((12.0 * k).clamp(10.0, 14.0)),
            pal.muted,
        );
    }
}

/// One cubic segment per hop between ports. The bend needs a minimum reach, 30 points at
/// full zoom, so short hops still curve.
fn bezier_pts(pts: &[Pos2], k: f32) -> Vec<[Pos2; 4]> {
    let mut out = vec![];
    for w in pts.windows(2) {
        let (a, b) = (w[0], w[1]);
        let dir = if b.x >= a.x { 1.0 } else { -1.0 };
        let dx = ((b.x - a.x).abs() / 2.0).max(30.0 * k) * dir;
        out.push([a, Pos2::new(a.x + dx, a.y), Pos2::new(b.x - dx, b.y), b]);
    }
    out
}

fn draw_edge(
    painter: &Painter,
    e: &SEdge,
    to_screen: &impl Fn([f32; 2]) -> Pos2,
    color: Color32,
    width: f32,
    dash: Option<(f32, f32)>,
    k: f32,
) {
    let pts: Vec<Pos2> = e.pts.iter().map(|&p| to_screen(p)).collect();
    let segs = bezier_pts(&pts, k);
    match dash {
        Some((dl, gap)) => {
            let mut flat: Vec<Pos2> = vec![];
            for s in &segs {
                flat.extend(CubicBezierShape::from_points_stroke(*s, false, Color32::TRANSPARENT, Stroke::NONE).flatten(Some(0.6)));
            }
            painter.extend(Shape::dashed_line(&flat, super::theme::stroke(width, color), dl * k.clamp(0.7, 1.2), gap * k.clamp(0.7, 1.2)));
        }
        None => {
            for s in &segs {
                painter.add(Shape::CubicBezier(CubicBezierShape::from_points_stroke(
                    *s,
                    false,
                    Color32::TRANSPARENT,
                    super::theme::stroke(width, color),
                )));
            }
        }
    }
    if k > 0.3 {
        if let Some(last) = segs.last() {
            let tip = last[3];
            let dir = (tip - last[2]).normalized();
            let dir = if dir.x.is_nan() { Vec2::X } else { dir };
            let n = Vec2::new(-dir.y, dir.x);
            let s = 7.0 * k.clamp(0.7, 1.3);
            painter.add(Shape::convex_polygon(
                vec![tip, tip - dir * s + n * (s * 0.45), tip - dir * s - n * (s * 0.45)],
                color,
                Stroke::NONE,
            ));
        }
    }
}

fn near_polyline(pts: &[[f32; 2]], p: Pos2, to_screen: &impl Fn([f32; 2]) -> Pos2, dist: f32) -> bool {
    pts.windows(2).any(|w| {
        let (a, b) = (to_screen(w[0]), to_screen(w[1]));
        let ab = b - a;
        let len2 = ab.length_sq();
        let t = if len2 == 0.0 { 0.0 } else { ((p - a).dot(ab) / len2).clamp(0.0, 1.0) };
        (a + ab * t).distance(p) <= dist
    })
}
