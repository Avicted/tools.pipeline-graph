//! String interning: every id, label, script and repo name is stored once and
//! referred to by a `Sym`, which keeps a 50k-node graph small.

use std::collections::HashMap;
use std::sync::Arc;

#[derive(Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord, Debug, Default)]
pub struct Sym(pub u32);

pub struct Interner {
    map: HashMap<Arc<str>, Sym>,
    strs: Vec<Arc<str>>,
}

impl Default for Interner {
    fn default() -> Self {
        Self::new()
    }
}

impl Interner {
    pub fn new() -> Self {
        let mut i = Interner { map: HashMap::new(), strs: Vec::new() };
        i.intern(""); // Sym(0) is always the empty string
        i
    }

    pub fn intern(&mut self, s: &str) -> Sym {
        if let Some(&sym) = self.map.get(s) {
            return sym;
        }
        let a: Arc<str> = Arc::from(s);
        let sym = Sym(self.strs.len() as u32);
        self.strs.push(a.clone());
        self.map.insert(a, sym);
        sym
    }

    pub fn lookup(&self, s: &str) -> Option<Sym> {
        self.map.get(s).copied()
    }

    pub fn len(&self) -> usize {
        self.strs.len()
    }

    pub fn is_empty(&self) -> bool {
        self.strs.len() <= 1
    }

    pub fn shrink(&mut self) {
        self.map.shrink_to_fit();
        self.strs.shrink_to_fit();
    }
}

impl std::ops::Index<Sym> for Interner {
    type Output = str;
    fn index(&self, s: Sym) -> &str {
        &self.strs[s.0 as usize]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn interns_once() {
        let mut i = Interner::new();
        let a = i.intern("hello");
        assert_eq!(a, i.intern("hello"));
        assert_ne!(a, i.intern("world"));
        assert_eq!(&i[a], "hello");
        assert_eq!(i.intern(""), Sym(0));
        assert_eq!(i.lookup("nope"), None);
    }
}
