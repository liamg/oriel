#[test]
fn quick() {}

#[test]
fn slow() {
    std::thread::sleep(std::time::Duration::from_secs(90));
}
