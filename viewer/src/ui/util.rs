//! Small helpers: forge links, percent-encoding for deep links, number formatting,
//! and clickable text that never starts a selection.

/// Text that acts as a link: a click target that never starts a text selection.
pub fn link(ui: &mut egui::Ui, text: impl Into<egui::WidgetText>) -> egui::Response {
    ui.add(egui::Label::new(text).selectable(false).sense(egui::Sense::click())).on_hover_cursor(egui::CursorIcon::PointingHand)
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
    if host.is_empty() || path.is_empty() {
        return None;
    }
    Some(Forge { base: format!("https://{}/{}", host.to_lowercase(), path), host: host.to_lowercase() })
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
    Some(format!("{}{}{}/{}{}", f.base, blob_kind(&f.host), commit, file.trim_start_matches("./"), anchor))
}

pub fn commit_url(repo_url: &str, commit: &str) -> Option<String> {
    let f = forge(repo_url)?;
    if commit.is_empty() {
        return None;
    }
    Some(format!("{}/commit/{}", f.base, commit))
}

pub fn short(s: &str) -> &str {
    &s[..s.len().min(8)]
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

pub fn decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%' && i + 2 < b.len() {
            if let Ok(v) = u8::from_str_radix(&s[i + 1..i + 3], 16) {
                out.push(v);
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
}
