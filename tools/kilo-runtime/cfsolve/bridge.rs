//! A stdin/stdout wrapper around the verified cfsolve library, with no artifact
//! sink and no proxy credential on argv. Built against the supplied cfsolve tree.
use cfsolve::{Browser, Options, PageSource, Turnstile, Widget};
use serde::Deserialize;
use std::io::{self, Read};

#[derive(Deserialize)]
struct Input {
    #[serde(default)]
    proxy: String,
    profile: String,
    #[serde(default)]
    check_only: bool,
}

fn main() {
    if run().is_err() {
        println!("{{\"error_code\":\"TURNSTILE_UNAVAILABLE\"}}");
        std::process::exit(1);
    }
}
fn run() -> Result<(), Box<dyn std::error::Error>> {
    let mut raw = String::new();
    io::stdin().take(16385).read_to_string(&mut raw)?;
    if raw.len() > 16384 {
        return Err("request too large".into());
    }
    let input: Input = serde_json::from_str(&raw)?;
    let browser = Browser::read(&input.profile)?;
    if input.check_only {
        println!("{}", serde_json::json!({"ready": true}));
        return Ok(());
    }
    let proxy = cfsolve::Url::parse(&input.proxy)?;
    if !["http", "https", "socks5", "socks5h"].contains(&proxy.scheme())
        || proxy.host_str().is_none()
    {
        return Err("invalid proxy".into());
    }
    let options = Options {
        proxy: Some(input.proxy),
        trusted_root: None,
        sink: None,
    };
    let request = Turnstile {
        url: "https://app.kilo.ai/users/sign_in".parse()?,
        page: PageSource::Navigate,
        widget: Some(Widget {
            sitekey: "0x4AAAAAAB1etOa0J3__watr".into(),
            action: None,
            cdata: None,
        }),
    };
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()?;
    let token = runtime.block_on(browser.solve_turnstile(request, &options))?;
    println!("{}", serde_json::json!({"token": token}));
    Ok(())
}
