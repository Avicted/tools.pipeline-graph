//! Colours and egui styling. One place, so light and dark stay consistent.

use crate::core::derive::Class;
use crate::core::graph::{Ek, Kind};
use egui::{Color32, CornerRadius, FontFamily, FontId, Stroke, TextStyle, Visuals};

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Mode {
    Light,
    Dark,
}

pub struct Palette {
    pub dark: bool,
    pub bg: Color32,
    pub panel: Color32,
    pub card: Color32,
    pub card_sel: Color32,
    pub card_sel_text: Color32,
    pub text: Color32,
    pub muted: Color32,
    pub line: Color32,
    pub lane: Color32,
    pub lane_alt: Color32,
    pub accent: Color32,
    pub up: Color32,
    pub down: Color32,
    pub added: Color32,
    pub changed: Color32,
    pub removed: Color32,
    pub warn: Color32,
}

pub const fn rgb(r: u8, g: u8, b: u8) -> Color32 {
    Color32::from_rgb(r, g, b)
}

pub fn palette(m: Mode) -> Palette {
    match m {
        Mode::Light => Palette {
            dark: false,
            bg: rgb(0xFA, 0xFA, 0xF8),
            panel: rgb(0xF3, 0xF3, 0xF0),
            card: rgb(0xFF, 0xFF, 0xFF),
            card_sel: rgb(0x16, 0x1A, 0x22),
            card_sel_text: rgb(0xFF, 0xFF, 0xFF),
            text: rgb(0x16, 0x1A, 0x22),
            muted: rgb(0x6B, 0x72, 0x80),
            line: rgb(0xDD, 0xDD, 0xD8),
            lane: rgb(0xF2, 0xF2, 0xEE),
            lane_alt: rgb(0xFA, 0xFA, 0xF7),
            accent: rgb(0x25, 0x63, 0xEB),
            up: rgb(0x7C, 0x3A, 0xED),
            down: rgb(0xDB, 0x27, 0x77),
            added: rgb(0x16, 0xA3, 0x4A),
            changed: rgb(0xD9, 0x77, 0x06),
            removed: rgb(0xDC, 0x26, 0x26),
            warn: rgb(0xC2, 0x41, 0x0C),
        },
        Mode::Dark => Palette {
            dark: true,
            bg: rgb(0x14, 0x16, 0x1B),
            panel: rgb(0x1B, 0x1E, 0x25),
            card: rgb(0x22, 0x26, 0x2E),
            card_sel: rgb(0xF3, 0xF4, 0xF6),
            card_sel_text: rgb(0x11, 0x14, 0x1A),
            text: rgb(0xE5, 0xE7, 0xEB),
            muted: rgb(0x9C, 0xA3, 0xAF),
            line: rgb(0x33, 0x38, 0x42),
            lane: rgb(0x1A, 0x1D, 0x23),
            lane_alt: rgb(0x17, 0x19, 0x1F),
            accent: rgb(0x60, 0xA5, 0xFA),
            up: rgb(0xA7, 0x8B, 0xFA),
            down: rgb(0xF4, 0x72, 0xB6),
            added: rgb(0x4A, 0xDE, 0x80),
            changed: rgb(0xFB, 0xBF, 0x24),
            removed: rgb(0xF8, 0x71, 0x71),
            warn: rgb(0xFB, 0x92, 0x3C),
        },
    }
}

pub fn kind_color(k: Kind, composite: bool, dark: bool) -> Color32 {
    match (k, composite, dark) {
        (Kind::Build, true, false) => rgb(0x7C, 0x3A, 0xED),
        (Kind::Build, true, true) => rgb(0xA7, 0x8B, 0xFA),
        (Kind::Build, false, false) => rgb(0x25, 0x63, 0xEB),
        (Kind::Build, false, true) => rgb(0x60, 0xA5, 0xFA),
        (Kind::External, _, false) => rgb(0xEA, 0x58, 0x0C),
        (Kind::External, _, true) => rgb(0xFB, 0x92, 0x3C),
        (Kind::Make, _, false) => rgb(0x64, 0x74, 0x8B),
        (Kind::Make, _, true) => rgb(0x94, 0xA3, 0xB8),
        (Kind::Image, _, false) => rgb(0x05, 0x96, 0x69),
        (Kind::Image, _, true) => rgb(0x34, 0xD3, 0x99),
    }
}

pub fn class_color(c: Class, dark: bool) -> Color32 {
    let (l, d) = match c {
        Class::Build => (rgb(0x47, 0x55, 0x69), rgb(0x94, 0xA3, 0xB8)),
        Class::Test => (rgb(0x0D, 0x94, 0x88), rgb(0x2D, 0xD4, 0xBF)),
        Class::Lint => (rgb(0x64, 0x74, 0x8B), rgb(0xCB, 0xD5, 0xE1)),
        Class::Scan => (rgb(0xB4, 0x53, 0x09), rgb(0xFB, 0xBF, 0x24)),
        Class::Package => (rgb(0x08, 0x91, 0xB2), rgb(0x22, 0xD3, 0xEE)),
        Class::Publish => (rgb(0x4F, 0x46, 0xE5), rgb(0x81, 0x8C, 0xF8)),
        Class::Deploy => (rgb(0xBE, 0x18, 0x5D), rgb(0xF4, 0x72, 0xB6)),
        Class::Other | Class::None => (rgb(0x9C, 0xA3, 0xAF), rgb(0x6B, 0x72, 0x80)),
    };
    if dark {
        d
    } else {
        l
    }
}

pub struct EdgeStyle {
    pub color: Color32,
    pub width: f32,
    pub dash: Option<(f32, f32)>,
}

pub fn edge_style(k: Ek, dark: bool) -> EdgeStyle {
    let c = |l: Color32, d: Color32| if dark { d } else { l };
    match k {
        Ek::Snapshot => EdgeStyle { color: c(rgb(0x47, 0x55, 0x69), rgb(0x94, 0xA3, 0xB8)), width: 1.5, dash: None },
        Ek::Artifact => EdgeStyle { color: c(rgb(0x47, 0x55, 0x69), rgb(0x94, 0xA3, 0xB8)), width: 1.5, dash: Some((6.0, 4.0)) },
        Ek::Trigger => EdgeStyle { color: c(rgb(0xEA, 0x58, 0x0C), rgb(0xFB, 0x92, 0x3C)), width: 2.2, dash: None },
        Ek::Produces => EdgeStyle { color: c(rgb(0x05, 0x96, 0x69), rgb(0x34, 0xD3, 0x99)), width: 1.5, dash: Some((2.0, 4.0)) },
        Ek::Consumes => EdgeStyle { color: c(rgb(0x05, 0x96, 0x69), rgb(0x34, 0xD3, 0x99)), width: 1.5, dash: Some((2.0, 4.0)) },
        Ek::Derives => EdgeStyle { color: c(rgb(0x0D, 0x94, 0x88), rgb(0x2D, 0xD4, 0xBF)), width: 1.8, dash: None },
        Ek::RunsIn => EdgeStyle { color: c(rgb(0x08, 0x91, 0xB2), rgb(0x22, 0xD3, 0xEE)), width: 1.4, dash: Some((5.0, 3.0)) },
        Ek::Runs => EdgeStyle { color: c(rgb(0x64, 0x74, 0x8B), rgb(0x94, 0xA3, 0xB8)), width: 1.2, dash: None },
        Ek::Depends | Ek::Calls | Ek::Other => {
            EdgeStyle { color: c(rgb(0x94, 0xA3, 0xB8), rgb(0x64, 0x74, 0x8B)), width: 1.0, dash: Some((4.0, 4.0)) }
        }
    }
}

/// Fira Sans for text, DejaVu Sans behind it so arrows and symbols in job names always draw.
pub fn install_fonts(ctx: &egui::Context) {
    let mut f = egui::FontDefinitions::default();
    f.font_data
        .insert("fira".into(), std::sync::Arc::new(egui::FontData::from_static(include_bytes!("../../assets/fonts/FiraSans-Regular.ttf"))));
    f.font_data
        .insert("dejavu".into(), std::sync::Arc::new(egui::FontData::from_static(include_bytes!("../../assets/fonts/DejaVuSans.ttf"))));
    let prop = f.families.entry(FontFamily::Proportional).or_default();
    prop.insert(0, "fira".into());
    prop.insert(1, "dejavu".into());
    f.families.entry(FontFamily::Monospace).or_default().insert(1, "dejavu".into());
    ctx.set_fonts(f);
}

pub fn apply(ctx: &egui::Context, mode: Mode) {
    let p = palette(mode);
    let mut v = if p.dark { Visuals::dark() } else { Visuals::light() };
    v.panel_fill = p.panel;
    v.window_fill = p.card;
    v.extreme_bg_color = if p.dark { p.bg } else { p.card }; // text fields: lighter than the page
    v.faint_bg_color = if p.dark { rgb(0x1E, 0x21, 0x28) } else { rgb(0xF6, 0xF6, 0xF3) };
    v.override_text_color = Some(p.text);
    v.selection.bg_fill = if p.dark { rgb(0x1E, 0x3A, 0x8A) } else { rgb(0xDB, 0xEA, 0xFE) };
    v.selection.stroke = stroke(1.0, p.accent);
    v.hyperlink_color = p.accent;
    v.window_stroke = stroke(1.0, p.line);
    v.window_corner_radius = CornerRadius::same(10);
    v.menu_corner_radius = CornerRadius::same(8);
    v.widgets.noninteractive.bg_stroke = stroke(1.0, p.line);
    v.widgets.inactive.bg_stroke = stroke(1.0, p.line);
    for w in [&mut v.widgets.inactive, &mut v.widgets.hovered, &mut v.widgets.active, &mut v.widgets.open] {
        w.corner_radius = CornerRadius::same(6);
    }
    v.widgets.noninteractive.corner_radius = CornerRadius::same(6);
    v.widgets.inactive.weak_bg_fill = if p.dark { rgb(0x27, 0x2B, 0x34) } else { rgb(0xEC, 0xEC, 0xE8) };
    v.widgets.inactive.bg_fill = v.widgets.inactive.weak_bg_fill;
    v.widgets.hovered.weak_bg_fill = if p.dark { rgb(0x32, 0x37, 0x42) } else { rgb(0xE2, 0xE2, 0xDD) };
    v.popup_shadow = egui::Shadow { offset: [0, 6], blur: 18, spread: 0, color: Color32::from_black_alpha(if p.dark { 120 } else { 40 }) };
    ctx.set_visuals(v);
    ctx.style_mut(|s| {
        s.spacing.item_spacing = egui::vec2(8.0, 6.0);
        s.spacing.button_padding = egui::vec2(10.0, 5.0);
        s.spacing.interact_size.y = 26.0;
        s.spacing.scroll.bar_width = 8.0;
        // Labels that act as buttons must not start a text selection on drag. Text worth
        // copying (ids, sources, scripts) opts back in with `Label::selectable(true)`.
        s.interaction.selectable_labels = false;
        s.text_styles = [
            (TextStyle::Heading, FontId::new(20.0, FontFamily::Proportional)),
            (TextStyle::Body, FontId::new(14.0, FontFamily::Proportional)),
            (TextStyle::Button, FontId::new(14.0, FontFamily::Proportional)),
            (TextStyle::Small, FontId::new(12.0, FontFamily::Proportional)),
            (TextStyle::Monospace, FontId::new(13.0, FontFamily::Monospace)),
        ]
        .into();
    });
}

pub fn with_alpha(c: Color32, a: u8) -> Color32 {
    Color32::from_rgba_unmultiplied(c.r(), c.g(), c.b(), a)
}

/// `Stroke::new` with a plain `f32` width, so literals do not fall back from `f64`.
pub fn stroke(width: f32, color: Color32) -> Stroke {
    Stroke { width, color }
}
