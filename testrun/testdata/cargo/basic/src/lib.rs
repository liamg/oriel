pub fn add(a: i32, b: i32) -> i32 {
    a + b
}

pub fn sign(n: i32) -> i32 {
    if n < 0 {
        return -1;
    }
    1
}

pub mod shapes;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn passes() {
        assert_eq!(add(1, 2), 3);
    }

    #[test]
    fn fails() {
        println!("about to add");
        assert_eq!(add(2, 2), 5);
    }

    #[test]
    fn panics() {
        let v: Vec<i32> = Vec::new();
        let _ = v[3];
    }

    #[test]
    #[ignore]
    fn ignored() {}

    #[test]
    #[should_panic(expected = "boom")]
    fn should_panic() {
        panic!("boom");
    }

    mod inner {
        use super::super::*;

        #[test]
        fn deep() {
            assert!(sign(-1) < 0, "sign of -1 was {}", sign(-1));
        }
    }
}
