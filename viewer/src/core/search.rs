//! Search over names, ids, scripts, parameters and triggers. Plain substring
//! tests over two flat lowercase buffers (names, bodies) answer in a few
//! milliseconds at 50k nodes; a fuzzy matcher only runs when nothing matched.

use super::derive::{Class, Derived, ENTRY_CHAINED, ENTRY_MANUAL, ENTRY_SCHEDULE, ENTRY_VCS};
use super::graph::{Graph, Kind};
use nucleo_matcher::pattern::{CaseMatching, Normalization, Pattern};
use nucleo_matcher::{Config, Matcher, Utf32Str};

const BODY_CAP: usize = 2000; // characters of script text indexed per step

pub struct SearchIndex {
    names: String,
    name_off: Vec<u32>,
    label_len: Vec<u16>,
    body: String,
    body_off: Vec<u32>,
}

/// `is:` filters: facts the snapshot page counts.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Flag {
    Isolated,
    NoTest,
    Container,
    Undefined,
}

#[derive(Default, Clone, PartialEq, Debug)]
pub struct Filters {
    pub is: Option<Flag>,
    pub repo: Option<String>,
    pub kind: Option<Kind>,
    pub class: Option<Class>,
    pub trig: Option<u8>,
}

#[derive(Default, Clone, Debug)]
pub struct Query {
    pub terms: Vec<String>,
    pub filters: Filters,
}

impl Query {
    /// Splits `text` into search terms and `repo:`, `kind:`, `class:`, `trig:` filters.
    pub fn parse(text: &str) -> Query {
        let mut q = Query::default();
        for tok in text.split_whitespace() {
            let low = tok.to_lowercase();
            match low.split_once(':') {
                // a filter still being typed (`repo:`) filters nothing yet
                Some(("repo" | "kind" | "class" | "trig" | "is", "")) => {}
                Some(("repo", v)) => q.filters.repo = Some(v.to_string()),
                Some(("kind", v)) => {
                    q.filters.kind = match v {
                        "build" | "job" => Some(Kind::Build),
                        "make" => Some(Kind::Make),
                        "image" => Some(Kind::Image),
                        "external" => Some(Kind::External),
                        _ => None,
                    }
                }
                Some(("class", v)) => q.filters.class = Class::parse(v),
                Some(("is", v)) => {
                    q.filters.is = match v {
                        "isolated" | "standalone" => Some(Flag::Isolated),
                        "notest" => Some(Flag::NoTest),
                        "container" => Some(Flag::Container),
                        "undefined" => Some(Flag::Undefined),
                        _ => None,
                    }
                }
                Some(("trig", v)) => {
                    q.filters.trig = match v {
                        "vcs" | "commit" => Some(ENTRY_VCS),
                        "schedule" | "scheduled" => Some(ENTRY_SCHEDULE),
                        "chained" | "finish" => Some(ENTRY_CHAINED),
                        "manual" => Some(ENTRY_MANUAL),
                        _ => None,
                    }
                }
                _ => q.terms.push(low),
            }
        }
        q
    }

    pub fn is_empty(&self) -> bool {
        self.terms.is_empty() && self.filters == Filters::default()
    }
}

#[derive(Clone, Copy, Debug)]
pub struct Hit {
    pub node: u32,
    pub score: u32,
}

impl SearchIndex {
    pub fn build(g: &Graph) -> SearchIndex {
        let n = g.len();
        let mut ix = SearchIndex {
            names: String::new(),
            name_off: Vec::with_capacity(n + 1),
            label_len: Vec::with_capacity(n),
            body: String::new(),
            body_off: Vec::with_capacity(n + 1),
        };
        for v in 0..n as u32 {
            ix.name_off.push(ix.names.len() as u32);
            let label = g.node_label(v).to_lowercase();
            ix.label_len.push(label.len().min(u16::MAX as usize) as u16);
            ix.names.push_str(&label);
            for part in [g.s(g.sub[v as usize]), g.node_id(v), g.node_repo(v), g.s(g.project[v as usize])] {
                ix.names.push('\u{1}');
                ix.names.push_str(&part.to_lowercase());
            }
            ix.body_off.push(ix.body.len() as u32);
            for st in g.steps(v) {
                for part in [g.s(st.name), g.s(st.summary), g.s(st.script)] {
                    let end = part.char_indices().nth(BODY_CAP).map_or(part.len(), |(i, _)| i);
                    ix.body.push_str(&part[..end].to_lowercase());
                    ix.body.push('\u{1}');
                }
            }
            for (k, val) in g.params(v) {
                ix.body.push_str(&format!("{}={}\u{1}", g.s(*k).to_lowercase(), g.s(*val).to_lowercase()));
            }
            for a in g.artifacts(v) {
                ix.body.push_str(&g.s(*a).to_lowercase());
                ix.body.push('\u{1}');
            }
            for t in g.triggers(v) {
                ix.body.push_str(&format!("{} {}\u{1}", g.s(t.ty).to_lowercase(), g.s(t.detail).to_lowercase()));
            }
        }
        ix.name_off.push(ix.names.len() as u32);
        ix.body_off.push(ix.body.len() as u32);
        ix
    }

    fn name(&self, v: u32) -> &str {
        &self.names[self.name_off[v as usize] as usize..self.name_off[v as usize + 1] as usize]
    }

    fn body(&self, v: u32) -> &str {
        &self.body[self.body_off[v as usize] as usize..self.body_off[v as usize + 1] as usize]
    }

    fn passes(&self, g: &Graph, d: &Derived, f: &Filters, v: u32) -> bool {
        let vi = v as usize;
        f.kind.is_none_or(|k| g.kind[vi] == k)
            && f.class.is_none_or(|c| d.class[vi] == c)
            && f.trig.is_none_or(|t| d.entry[vi] & t != 0)
            && f.is.is_none_or(|x| match x {
                Flag::Isolated => d.isolated[vi],
                Flag::NoTest => d.no_test_up[vi],
                Flag::Container => d.container[vi],
                Flag::Undefined => g.s(g.sub[vi]).ends_with("(no rule)"),
            })
            && f.repo.as_ref().is_none_or(|r| g.node_repo(v).to_lowercase().contains(r.as_str()))
    }

    /// Cost of matching `term` in node `v`: lower is better, `None` is no match.
    fn tier(&self, v: u32, term: &str) -> Option<u32> {
        let name = self.name(v);
        let label = &name[..self.label_len[v as usize] as usize];
        if label.starts_with(term) {
            Some(0)
        } else if label.contains(term) {
            Some(1)
        } else if name.contains(term) {
            Some(2)
        } else if self.body(v).contains(term) {
            Some(3)
        } else {
            None
        }
    }

    /// Every node matching `q`, best first.
    pub fn search(&self, g: &Graph, d: &Derived, q: &Query) -> Vec<Hit> {
        let mut hits = Vec::new();
        for v in 0..g.len() as u32 {
            if !self.passes(g, d, &q.filters, v) {
                continue;
            }
            let mut score = 0;
            let mut ok = true;
            for t in &q.terms {
                match self.tier(v, t) {
                    Some(s) => score += s,
                    None => {
                        ok = false;
                        break;
                    }
                }
            }
            if ok {
                hits.push(Hit { node: v, score });
            }
        }
        if hits.is_empty() && q.terms.len() == 1 && q.terms[0].chars().count() >= 3 {
            hits = self.fuzzy(g, d, q);
        }
        hits.sort_by_key(|h| (h.score, g.kind[h.node as usize] == Kind::Make, self.label_len[h.node as usize], h.node));
        hits
    }

    fn fuzzy(&self, g: &Graph, d: &Derived, q: &Query) -> Vec<Hit> {
        let mut m = Matcher::new(Config::DEFAULT);
        let pat = Pattern::parse(&q.terms[0], CaseMatching::Ignore, Normalization::Smart);
        let mut buf = Vec::new();
        let mut out = Vec::new();
        for v in 0..g.len() as u32 {
            if !self.passes(g, d, &q.filters, v) {
                continue;
            }
            let label = &self.name(v)[..self.label_len[v as usize] as usize];
            if let Some(s) = pat.score(Utf32Str::new(label, &mut buf), &mut m) {
                out.push(Hit { node: v, score: 1000 + u32::MAX / 2 - s.min(u32::MAX / 2) });
            }
        }
        out
    }
}

/// Fuzzy-ranks short strings (command palette entries); best first.
pub fn fuzzy_rank<'a>(query: &str, items: impl IntoIterator<Item = (usize, &'a str)>) -> Vec<(usize, u32)> {
    let mut m = Matcher::new(Config::DEFAULT);
    let pat = Pattern::parse(query, CaseMatching::Ignore, Normalization::Smart);
    let mut buf = Vec::new();
    let mut out: Vec<(usize, u32)> =
        items.into_iter().filter_map(|(i, s)| pat.score(Utf32Str::new(s, &mut buf), &mut m).map(|sc| (i, sc))).collect();
    out.sort_by_key(|&(i, sc)| (std::cmp::Reverse(sc), i));
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::core::graph::tests::sample;

    fn find(g: &Graph, d: &Derived, ix: &SearchIndex, q: &str) -> Vec<String> {
        ix.search(g, d, &Query::parse(q)).iter().map(|h| g.node_label(h.node).to_string()).collect()
    }

    #[test]
    fn ranks_names_before_bodies_and_applies_filters() {
        let g = sample();
        let d = Derived::compute(&g);
        let ix = SearchIndex::build(&g);
        assert_eq!(find(&g, &d, &ix, "lint")[0], "Lint");
        assert_eq!(find(&g, &d, &ix, "docker build"), ["Base image"], "script text is searched");
        assert_eq!(find(&g, &d, &ix, "repo:r2 kind:build").len(), 3);
        assert_eq!(find(&g, &d, &ix, "class:deploy"), ["Deploy prod"]);
        assert_eq!(find(&g, &d, &ix, "trig:vcs"), ["Lint"]);
        assert_eq!(find(&g, &d, &ix, "is:isolated"), ["Lonely"]);
        assert_eq!(find(&g, &d, &ix, "a=1"), ["App build & test"], "parameters are searched");
        assert!(find(&g, &d, &ix, "zzzz").is_empty());
        assert_eq!(find(&g, &d, &ix, "repo:").len(), g.len(), "an unfinished filter keeps everything");
    }

    #[test]
    fn fuzzy_only_when_nothing_matched() {
        let g = sample();
        let d = Derived::compute(&g);
        let ix = SearchIndex::build(&g);
        assert_eq!(find(&g, &d, &ix, "dplyprd"), ["Deploy prod"]);
    }

    #[test]
    fn palette_ranking() {
        let r = fuzzy_rank("tbl", [(0, "Go to Table"), (1, "Toggle theme")]);
        assert_eq!(r[0].0, 0);
    }
}
