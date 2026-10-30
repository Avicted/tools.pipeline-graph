//! A uniform grid over a layout, so the canvas only touches what is on screen.

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Rect {
    pub x0: f32,
    pub y0: f32,
    pub x1: f32,
    pub y1: f32,
}

impl Rect {
    pub fn new(x: f32, y: f32, w: f32, h: f32) -> Rect {
        Rect { x0: x, y0: y, x1: x + w, y1: y + h }
    }
    pub fn intersects(&self, o: &Rect) -> bool {
        self.x0 <= o.x1 && o.x0 <= self.x1 && self.y0 <= o.y1 && o.y0 <= self.y1
    }
    pub fn contains(&self, x: f32, y: f32) -> bool {
        x >= self.x0 && x <= self.x1 && y >= self.y0 && y <= self.y1
    }
    pub fn center(&self) -> (f32, f32) {
        ((self.x0 + self.x1) / 2.0, (self.y0 + self.y1) / 2.0)
    }
    pub fn w(&self) -> f32 {
        self.x1 - self.x0
    }
    pub fn h(&self) -> f32 {
        self.y1 - self.y0
    }
}

const CELL: f32 = 512.0;

pub struct Grid {
    cols: usize,
    rows: usize,
    cells: Vec<Vec<u32>>,
}

impl Grid {
    pub fn new(w: f32, h: f32) -> Grid {
        let cols = ((w / CELL).ceil() as usize).max(1);
        let rows = ((h / CELL).ceil() as usize).max(1);
        Grid { cols, rows, cells: vec![Vec::new(); cols * rows] }
    }

    fn span(&self, r: &Rect) -> (usize, usize, usize, usize) {
        let c = |v: f32, max: usize| ((v.max(0.0) / CELL) as usize).min(max - 1);
        (c(r.x0, self.cols), c(r.y0, self.rows), c(r.x1, self.cols), c(r.y1, self.rows))
    }

    pub fn insert(&mut self, id: u32, r: &Rect) {
        let (c0, r0, c1, r1) = self.span(r);
        for row in r0..=r1 {
            for col in c0..=c1 {
                self.cells[row * self.cols + col].push(id);
            }
        }
    }

    /// Ids whose rectangle may intersect `view`, each exactly once. `seen` must have an entry per id.
    pub fn query(&self, view: &Rect, seen: &mut [u32], stamp: u32, out: &mut Vec<u32>) {
        let (c0, r0, c1, r1) = self.span(view);
        for row in r0..=r1 {
            for col in c0..=c1 {
                for &id in &self.cells[row * self.cols + col] {
                    if seen[id as usize] != stamp {
                        seen[id as usize] = stamp;
                        out.push(id);
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn finds_what_is_in_view_once() {
        let mut g = Grid::new(3000.0, 3000.0);
        g.insert(0, &Rect::new(10.0, 10.0, 100.0, 50.0));
        g.insert(1, &Rect::new(1000.0, 1000.0, 2000.0, 100.0)); // spans several cells
        g.insert(2, &Rect::new(2900.0, 2900.0, 50.0, 50.0));
        let mut seen = vec![0; 3];
        let mut out = vec![];
        g.query(&Rect::new(900.0, 900.0, 2000.0, 400.0), &mut seen, 1, &mut out);
        out.sort();
        assert_eq!(out, vec![1]);
        out.clear();
        g.query(&Rect::new(0.0, 0.0, 3000.0, 3000.0), &mut seen, 2, &mut out);
        out.sort();
        assert_eq!(out, vec![0, 1, 2]);
    }
}
