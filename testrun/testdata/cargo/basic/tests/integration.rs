#[test]
fn adds() {
    assert_eq!(basic::add(2, 3), 5);
}

#[test]
fn fails_too() {
    assert!(basic::sign(1) < 0);
}
