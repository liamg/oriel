//! Areas of shapes.
use std::collections::HashMap;
use std::fmt;

/// Anything with an area.
pub trait Shape {
    fn area(&self) -> f64;
}

#[derive(Debug, Clone)]
pub struct Circle {
    pub radius: f64,
}

impl Shape for Circle {
    fn area(&self) -> f64 {
        std::f64::consts::PI * self.radius.powi(2)
    }
}

pub enum Kind<'a> {
    Named(&'a str),
    Unit,
}

impl fmt::Display for Circle {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "circle r={}", self.radius)
    }
}

pub fn total(shapes: &[Box<dyn Shape>]) -> Result<f64, String> {
    let mut sum = 0.0_f64;
    let mut seen: HashMap<usize, bool> = HashMap::new();
    for (i, s) in shapes.iter().enumerate() {
        match s.area() {
            a if a < 0.0 => return Err(format!("shape {i}")),
            a => sum += a,
        }
        seen.insert(i, true);
    }
    let add = |x: f64| -> f64 { x + 1.0 };
    Ok(add(sum))
}
