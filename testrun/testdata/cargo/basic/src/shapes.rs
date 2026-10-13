pub fn area(w: i32, h: i32) -> i32 {
    w * h
}

#[cfg(test)]
mod tests {
    #[test]
    fn square() {
        assert_eq!(super::area(2, 2), 4);
    }
}
