//! Small helpers: forge links, percent-encoding for deep links, number formatting,
//! and the two widgets every screen shares: clickable text and explaining hover cards.

/// Text that acts as a link: a click target that never starts a text selection.
pub fn link(ui: &mut egui::Ui, text: impl Into<egui::WidgetText>) -> egui::Response {
    ui.add(egui::Label::new(text).selectable(false).sense(egui::Sense::click())).on_hover_cursor(egui::CursorIcon::PointingHand)
}

/// A hover card that explains what something means: a bold title and a short body.
pub fn hint(resp: egui::Response, title: &str, body: &str) -> egui::Response {
    resp.on_hover_ui(|ui| {
        ui.set_max_width(320.0);
        ui.label(egui::RichText::new(title).strong());
        ui.label(egui::RichText::new(body).weak());
    })
}

/// Where a repo's files live on the web, when its URL is a known forge.
pub struct Forge {
    pub base: String,
    pub host: String,
}

pub fn forge(url: &str) -> Option<Forge> {
    let u = url.trim();
    let (host, path) = if let Some(rest) = u.strip_prefix("git@") {
        rest.split_once(':')?
    } else {
        let rest = u
            .strip_prefix("https://")
            .or_else(|| u.strip_prefix("http://"))
            .or_else(|| u.strip_prefix("ssh://"))
            .or_else(|| u.strip_prefix("git+ssh://"))?;
        let rest = rest.split_once('@').map_or(rest, |(_, r)| r);
        let (h, p) = rest.split_once('/')?;
        (h.split(':').next()?, p)
    };
    let path = path.trim_end_matches('/').trim_end_matches(".git");
    // graph.json is not trusted: only a hostname may become the host of a link
    if host.is_empty() || path.is_empty() || !host.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-' | '_')) {
        return None;
    }
    Some(Forge { base: format!("https://{}/{}", host.to_lowercase(), encode(path)), host: host.to_lowercase() })
}

fn blob_kind(host: &str) -> &'static str {
    if host.contains("gitlab") {
        "/-/blob/"
    } else if host.contains("bitbucket") {
        "/src/"
    } else {
        "/blob/"
    }
}

/// A link to `file:line` at `commit`, or None when the host is not recognised.
pub fn source_url(repo_url: &str, commit: &str, source: &str) -> Option<String> {
    let f = forge(repo_url)?;
    if source.is_empty() || commit.is_empty() {
        return None;
    }
    let (file, line) = match source.rsplit_once(':') {
        Some((f, l)) if l.chars().all(|c| c.is_ascii_digit()) && !l.is_empty() => (f, Some(l)),
        _ => (source, None),
    };
    let anchor = match (line, f.host.contains("bitbucket")) {
        (Some(l), true) => format!("#lines-{l}"),
        (Some(l), false) => format!("#L{l}"),
        (None, _) => String::new(),
    };
    Some(format!("{}{}{}/{}{}", f.base, blob_kind(&f.host), encode_segment(commit), encode(file.trim_start_matches("./")), anchor))
}

pub fn commit_url(repo_url: &str, commit: &str) -> Option<String> {
    let f = forge(repo_url)?;
    if commit.is_empty() {
        return None;
    }
    Some(format!("{}/commit/{}", f.base, encode_segment(commit)))
}

/// The first 8 characters (a commit id is ASCII, but graph.json is not trusted to say so).
pub fn short(s: &str) -> &str {
    s.char_indices().nth(8).map_or(s, |(i, _)| &s[..i])
}

pub fn encode(s: &str) -> String {
    let mut o = String::new();
    for b in s.bytes() {
        if b.is_ascii_alphanumeric() || b"-_.~:/".contains(&b) {
            o.push(b as char);
        } else {
            o.push_str(&format!("%{b:02X}"));
        }
    }
    o
}

/// Like `encode`, but a `/` is escaped too: one path segment, such as a commit id.
fn encode_segment(s: &str) -> String {
    encode(s).replace('/', "%2F")
}

pub fn decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%' && i + 2 < b.len() {
            // bytes, not a str slice: i + 3 may fall inside a multi-byte character
            if let (Some(h), Some(l)) = ((b[i + 1] as char).to_digit(16), (b[i + 2] as char).to_digit(16)) {
                out.push((h * 16 + l) as u8);
                i += 3;
                continue;
            }
        }
        out.push(b[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// "1,234" style grouping for big counts.
pub fn num(n: u32) -> String {
    let s = n.to_string();
    let mut out = String::new();
    for (i, c) in s.chars().enumerate() {
        if i > 0 && (s.len() - i) % 3 == 0 {
            out.push(',');
        }
        out.push(c);
    }
    out
}

pub fn first_line(s: &str, max: usize) -> String {
    let l = s.lines().map(str::trim).find(|l| !l.is_empty()).unwrap_or("");
    if l.chars().count() > max {
        format!("{}…", l.chars().take(max.saturating_sub(1)).collect::<String>())
    } else {
        l.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn forge_links() {
        assert_eq!(forge("git@github.com:org/repo.git").unwrap().base, "https://github.com/org/repo");
        assert_eq!(forge("https://user@GitLab.com/g/r/").unwrap().base, "https://gitlab.com/g/r");
        assert!(forge("/local/dir").is_none());
        assert_eq!(
            source_url("git@github.com:o/r.git", "abc", ".teamcity/settings.kts:46").unwrap(),
            "https://github.com/o/r/blob/abc/.teamcity/settings.kts#L46"
        );
        assert!(source_url("git@github.com:o/r.git", "", "a:1").is_none());
        assert!(source_url("https://gitlab.com/o/r", "c", "f").unwrap().contains("/-/blob/c/f"));
    }

    #[test]
    fn percent_round_trip() {
        for s in ["bt:orders/Image", "a b&c=d", "ünï/cødé"] {
            assert_eq!(decode(&encode(s)), s);
        }
        assert_eq!(decode("100%"), "100%");
    }

    #[test]
    fn number_grouping() {
        assert_eq!(num(0), "0");
        assert_eq!(num(1234567), "1,234,567");
        assert_eq!(num(999), "999");
    }

    #[test]
    fn short_cuts_on_characters_not_bytes() {
        assert_eq!(short("0123456789abcdef"), "01234567");
        assert_eq!(short("abc"), "abc");
        assert_eq!(short(""), "");
        // graph.json is untrusted: a non-ASCII commit string must not panic
        assert_eq!(short("ééééééééééé"), "éééééééé");
        assert_eq!(short("🚀🚀🚀🚀🚀🚀🚀🚀🚀"), "🚀🚀🚀🚀🚀🚀🚀🚀");
    }

    #[test]
    fn decode_survives_percent_signs_before_multibyte_characters() {
        assert_eq!(decode("a%20b"), "a b");
        assert_eq!(decode("%41%42"), "AB");
        assert_eq!(decode("100%"), "100%");
        assert_eq!(decode("%4"), "%4");
        assert_eq!(decode("%zz"), "%zz");
        assert_eq!(decode("%é1"), "%é1");
        assert_eq!(decode("%1é"), "%1é");
        assert_eq!(decode("%🚀"), "%🚀");
        assert_eq!(decode(&encode("a b/é")), "a b/é");
    }

    // repos[].url, commit and source come from graph.json, so they are not trusted to be
    // well formed: whatever they hold must stay one path on the forge host they name.
    #[test]
    fn link_pieces_are_percent_encoded() {
        assert_eq!(
            source_url("https://github.com/o/r.git", "abc", "a b/c#d?e.kts:3").unwrap(),
            "https://github.com/o/r/blob/abc/a%20b/c%23d%3Fe.kts#L3"
        );
        assert_eq!(commit_url("https://github.com/o/r", "a/b?c#d").unwrap(), "https://github.com/o/r/commit/a%2Fb%3Fc%23d");
        assert_eq!(forge("https://github.com/o/r?x=1#f").unwrap().base, "https://github.com/o/r%3Fx%3D1%23f");
        // ordinary links are unchanged
        assert_eq!(
            source_url("git@github.com:org/repo.git", "0123abc", "ci/build.kts:10").unwrap(),
            "https://github.com/org/repo/blob/0123abc/ci/build.kts#L10"
        );
    }

    #[test]
    fn a_host_that_is_not_a_hostname_is_no_forge() {
        for url in ["https://exa mple.com/o/r", "git@h?x:o/r", "https://ex\u{202e}ample.com/o/r", "https://:@/o/r"] {
            assert!(forge(url).is_none(), "{url}");
        }
        assert!(forge("https://git.acme.io:7999/plat/base.git").is_some(), "a port is not part of the host");
        assert!(forge("git@gitlab.example-corp.com:g/p.git").is_some());
    }
}
