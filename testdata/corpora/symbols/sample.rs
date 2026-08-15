// Fixture crate for TestExtract_Rust: one async fn, one struct, one trait impl.

pub async fn fetch(url: &str) -> String {
    let mut out = String::new();
    out.push_str(url);
    out
}

pub struct Client {
    base: String,
    retries: u8,
}

impl Fetcher for Client {}
