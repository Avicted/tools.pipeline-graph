#!/usr/bin/env node
// Regenerates the screenshots of the HTML viewer used in the README.
//
//   make screenshots                                   (in Docker, the normal way)
//   make shot SCENE=focus,table                        (some scenes, into out/shots)
//   node scripts/screenshots.mjs [pipelines.html] [out dir] [--scenes file] [--only a,b]   (needs Node >= 22 and Chromium)
//
// Each scene opens the page in a fresh tab with the browser clock frozen and
// animations off, drives it with real keys and mouse events (falling back to
// the page's own functions where a gesture would be fragile), checks that the
// page reached the expected state and writes a PNG. Any JS error or failed
// check fails the run, so this doubles as a smoke test of the viewer.
// It also writes <out dir>/README.md, a gallery of every scene.

import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { parseArgs } from "node:util";

const { values, positionals } = parseArgs({ allowPositionals: true, options: { scenes: { type: "string", default: "scripts/scenes.json" }, only: { type: "string" } } });
const html = resolve(positionals[0] || "out/acme/pipelines.html");
const outDir = resolve(positionals[1] || "docs/screenshots");
const chrome = process.env.CHROME || "chromium";
const viewport = { width: 1920, height: 1080, deviceScaleFactor: 1 };

// Scenes live in scripts/scenes.json (written for the Acme example, examples/acme). Steps run in order:
//   key   - press a key          type  - type text into the focused input
//   eval  - run JS in the page   hover - move the mouse to the [x, y] the JS returns
//   check - JS that must be truthy, with a message for when it is not
const scenesFile = values.scenes;
const ALL = JSON.parse(readFileSync(resolve(scenesFile), "utf8"));
const names = values.only?.split(",").filter(Boolean);
const bad = (names || []).filter(n => !ALL.some(s => s.file === n + ".png"));
if (bad.length) { console.error(`unknown scene ${bad.join(", ")}; valid: ${ALL.map(s => s.file.replace(/\.png$/, "")).join(", ")}`); process.exit(2); }
const SCENES = names ? ALL.filter(s => names.includes(s.file.replace(/\.png$/, ""))) : ALL;

// ---- a minimal Chrome DevTools Protocol client --------------------------------
const sleep = ms => new Promise(r => setTimeout(r, ms));

async function launch() {
  const profile = mkdtempSync(join(tmpdir(), "pg-shots-"));
  const args = [
    "--headless=new", "--disable-gpu", "--hide-scrollbars", "--force-color-profile=srgb", "--font-render-hinting=none",
    "--no-first-run", "--no-default-browser-check", "--remote-debugging-port=0", `--user-data-dir=${profile}`,
    "--allow-file-access-from-files", "--disable-dev-shm-usage", "about:blank",
  ];
  if (process.env.CHROME_NO_SANDBOX) args.unshift("--no-sandbox");
  const proc = spawn(chrome, args, { stdio: ["ignore", "ignore", "pipe"] });
  const port = await new Promise((res, rej) => {
    let buf = "";
    proc.on("error", rej);
    proc.on("exit", code => rej(new Error(`${chrome} exited (${code}):\n${buf}`)));
    proc.stderr.on("data", d => {
      buf += d;
      const m = buf.match(/DevTools listening on ws:\/\/[^:]+:(\d+)\//);
      if (m) res(m[1]);
    });
  });
  const close = async () => {
    const exited = new Promise(r => proc.exitCode === null ? proc.once("exit", r) : r());
    proc.kill();
    await exited;
    rmSync(profile, { recursive: true, force: true, maxRetries: 5 });
  };
  return { port, close };
}

async function openTab(port) {
  const t = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: "PUT" })).json();
  const ws = new WebSocket(t.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  let seq = 0;
  const pending = new Map(), waiters = [], errors = [];
  ws.onmessage = m => {
    const d = JSON.parse(m.data);
    if (d.id && pending.has(d.id)) {
      const { res, rej } = pending.get(d.id);
      pending.delete(d.id);
      d.error ? rej(new Error(d.error.message)) : res(d.result);
    }
    if (d.method === "Runtime.exceptionThrown") errors.push(d.params.exceptionDetails.exception?.description || d.params.exceptionDetails.text);
    if (d.method === "Runtime.consoleAPICalled" && d.params.type === "error") errors.push(d.params.args.map(a => a.value ?? a.description).join(" "));
    for (const w of [...waiters]) if (w.method === d.method) { waiters.splice(waiters.indexOf(w), 1); w.res(d.params); }
  };
  const send = (method, params = {}) => new Promise((res, rej) => {
    pending.set(++seq, { res, rej });
    ws.send(JSON.stringify({ id: seq, method, params }));
  });
  const once = method => new Promise(res => waiters.push({ method, res }));
  const evaluate = async expr => {
    const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(`${expr}\n${r.exceptionDetails.exception?.description || r.exceptionDetails.text}`);
    return r.result.value;
  };
  return { send, once, evaluate, errors, close: () => { ws.close(); fetch(`http://127.0.0.1:${port}/json/close/${t.id}`).catch(() => {}); } };
}

const KEYS = { Escape: 27, Enter: 13, ArrowDown: 40, ArrowUp: 38, "/": 191 };
async function press(tab, key) {
  const base = { key, windowsVirtualKeyCode: KEYS[key] ?? key.toUpperCase().charCodeAt(0) };
  await tab.send("Input.dispatchKeyEvent", { type: "keyDown", ...base, text: key.length === 1 ? key : undefined });
  await tab.send("Input.dispatchKeyEvent", { type: "keyUp", ...base });
}

// ---- run ------------------------------------------------------------------------
// The page shows "generated N hours ago": freeze the clock two hours after the
// generation time baked into the page so the screenshots do not change daily.
const generated = readFileSync(html, "utf8").match(/"generated":"([^"]+)"/)?.[1] || "";
const now = (Date.parse(generated.replace(" UTC", "Z").replace(" ", "T")) || Date.now()) + 2 * 3600e3;

mkdirSync(outDir, { recursive: true });
const browser = await launch();
let failed = 0;
try {
  for (const s of SCENES) {
    const tab = await openTab(browser.port);
    try {
      await tab.send("Runtime.enable");
      await tab.send("Page.enable");
      await tab.send("Emulation.setDeviceMetricsOverride", { ...viewport, mobile: false });
      await tab.send("Emulation.setEmulatedMedia", { features: [
        { name: "prefers-color-scheme", value: s.theme }, { name: "prefers-reduced-motion", value: "reduce" },
      ] });
      // every scene starts from defaults: no remembered view, theme, sidebar or panel width
      await tab.send("Page.addScriptToEvaluateOnNewDocument", { source:
        `try { localStorage.clear(); localStorage.setItem("pg-hint", "1"); } catch (e) {}
         { const t = ${now}, D = Date; Date = class extends D { constructor(...a) { a.length ? super(...a) : super(t); } static now() { return t; } }; }` });
      const loaded = tab.once("Page.loadEventFired");
      await tab.send("Page.navigate", { url: pathToFileURL(html).href + "#" + s.hash });
      await loaded;
      await tab.evaluate("document.fonts.ready.then(() => true)");
      await sleep(300);
      for (const st of s.steps) {
        if (st.key) await press(tab, st.key);
        if (st.type) await tab.send("Input.insertText", { text: st.type });
        if (st.eval) await tab.evaluate(st.eval);
        if (st.hover) {
          const [x, y] = await tab.evaluate(st.hover);
          await tab.send("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
        }
        await sleep(st.wait ?? 250);
        if (st.check && !(await tab.evaluate(`!!(${st.check})`))) throw new Error(`check failed: ${st.msg}`);
      }
      if (tab.errors.length) throw new Error("page errors:\n" + tab.errors.join("\n"));
      const shot = await tab.send("Page.captureScreenshot", { format: "png" });
      writeFileSync(join(outDir, s.file), Buffer.from(shot.data, "base64"));
      console.log(`  ${relative(process.cwd(), join(outDir, s.file))}`);
    } catch (e) {
      failed++;
      console.error(`✗ ${s.file}: ${e.message}`);
    } finally {
      tab.close();
    }
  }
} finally {
  await browser.close();
}

const gallery = ["# Screenshots", "",
  "Generated by `make screenshots` from the [Acme example](../../examples/acme); do not edit by hand.", "",
  ...SCENES.flatMap(s => [`## ${s.title}`, "", s.caption, "", `![${s.title}](${s.file})`, ""])].join("\n");
if (!names) writeFileSync(join(outDir, "README.md"), gallery);

if (failed) {
  console.error(`${failed} of ${SCENES.length} screenshots failed`);
  process.exit(1);
}
