#[tokio::test]
async fn async_test() {}

#[test]
// a comment between
#[ignore]
fn ignored_with_comment() {}

fn not_a_test() {}

#[cfg(test)]
mod outer {
    mod inner {
        #[test]
        fn deep() {}
    }

    #[rstest::rstest]
    fn not_libtest() {}
}
