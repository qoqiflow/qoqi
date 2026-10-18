// qoqi.dev: the theme, copying code, the menu, what is on screen, and search.

(() => {
  const root = document.documentElement;
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch {} },
  };

  // The theme: the system's, until one is chosen
  const dark = () => root.dataset.theme !== "light";
  document.querySelectorAll("[data-theme-toggle]").forEach((b) => b.addEventListener("click", () => {
    root.dataset.theme = dark() ? "light" : "dark";
    store.set("theme", root.dataset.theme);
  }));

  // Copying: the command of the landing page, and each block of code
  const copy = async (button, text) => {
    try {
      await navigator.clipboard.writeText(text);
      const icon = button.querySelector("svg");
      if (!icon) button.textContent = "Copied";
      button.classList.add("done");
      setTimeout(() => { if (!icon) button.textContent = "Copy"; button.classList.remove("done"); }, 1400);
    } catch {}
  };
  document.querySelectorAll("[data-copy]").forEach((b) => b.addEventListener("click", () => {
    copy(b, b.closest("[data-copy-root]").querySelector("[data-copy-text]").textContent);
  }));
  document.querySelectorAll(".code").forEach((block) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "copy";
    b.textContent = "Copy";
    b.setAttribute("aria-label", "Copy the code");
    b.addEventListener("click", () => {
      let text = block.querySelector("pre").innerText;
      // Of a session at a shell, the commands alone
      if (block.dataset.lang === "shell" && /^\$ /m.test(text)) {
        text = text.split("\n").filter((l) => l.startsWith("$ ")).map((l) => l.slice(2)).join("\n");
      }
      copy(b, text.replace(/\n$/, ""));
    });
    block.appendChild(b);
  });

  // Headings that link to themselves
  document.querySelectorAll(".prose h2[id], .prose h3[id], .prose h4[id]").forEach((h) => {
    const a = document.createElement("a");
    a.className = "anchor";
    a.href = "#" + h.id;
    a.setAttribute("aria-label", "Link to this section");
    a.textContent = "#";
    h.appendChild(a);
  });

  // The menu of the documentation, on a narrow screen
  document.querySelectorAll("[data-nav-toggle]").forEach((b) => b.addEventListener("click", () => document.body.classList.toggle("nav-open")));
  document.addEventListener("click", (e) => {
    if (document.body.classList.contains("nav-open") && !e.target.closest("[data-nav], [data-nav-toggle]")) document.body.classList.remove("nav-open");
  });
  const current = document.querySelector(".sidebar a.on");
  if (current) current.scrollIntoView({ block: "center" });

  // The heading on screen, marked in the list at the side
  const toc = [...document.querySelectorAll(".toc a")];
  if (toc.length && "IntersectionObserver" in window) {
    const byId = new Map(toc.map((a) => [decodeURIComponent(a.hash.slice(1)), a]));
    const seen = new Set();
    const mark = () => {
      const heads = [...byId.keys()].map((id) => document.getElementById(id)).filter(Boolean);
      let on = heads.find((h) => seen.has(h.id)) || null;
      if (!on) on = heads.filter((h) => h.getBoundingClientRect().top < 120).pop() || null;
      toc.forEach((a) => a.classList.toggle("on", on !== null && a === byId.get(on.id)));
    };
    const io = new IntersectionObserver((entries) => {
      entries.forEach((e) => e.isIntersecting ? seen.add(e.target.id) : seen.delete(e.target.id));
      mark();
    }, { rootMargin: "-64px 0px -66% 0px" });
    byId.forEach((_, id) => { const h = document.getElementById(id); if (h) io.observe(h); });
  }

  // The flows of the landing page, as they run: their steps, their waits,
  // frozen, a crash, and what thaws them. One tab at a time, played while
  // it is on screen.
  const runs = [...document.querySelectorAll("[data-run]")];
  if (runs.length) {
    const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
    const pause = { ok: 1250, ice: 2000, crash: 1900, deploy: 1900, done: 4200 };
    let timer = 0, visible = false, active = runs[0], at = 0;

    const show = (run, i) => {
      const lines = [...run.querySelectorAll("[data-run-code] .line")];
      const events = [...run.querySelectorAll(".events li")];
      const ev = events[i].dataset;
      const ran = new Set(events.slice(0, i).filter((e) => e.dataset.kind === "ok").map((e) => +e.dataset.line));
      const line = +ev.line;
      lines.forEach((l, n) => {
        n += 1;
        l.classList.toggle("here", n === line && ev.kind !== "ice" && ev.kind !== "crash" && ev.kind !== "deploy");
        l.classList.toggle("cold", n === line && (ev.kind === "ice" || ev.kind === "deploy"));
        l.classList.toggle("hit", n === line && ev.kind === "crash");
        l.classList.toggle("ran", ran.has(n) && n !== line);
      });
      events.forEach((e, n) => { e.classList.toggle("on", n <= i); e.classList.toggle("now", n === i); });
      const state = run.querySelector("[data-run-state]");
      const name = { ice: "waiting", deploy: "waiting", crash: "down", done: "done" }[ev.kind] || "running";
      state.className = "state " + name;
      state.textContent = name;
      run.querySelector("[data-run-clock]").textContent = ev.clock;
      run.classList.toggle("down", ev.kind === "crash");
    };
    const play = () => {
      clearTimeout(timer);
      const events = active.querySelectorAll(".events li");
      show(active, at);
      if (still || !visible) return;
      timer = setTimeout(() => { at = (at + 1) % events.length; play(); }, pause[events[at].dataset.kind] || 1300);
    };
    const pick = (id) => {
      document.querySelectorAll("[data-tab]").forEach((t) => t.setAttribute("aria-selected", String(t.dataset.tab === id)));
      runs.forEach((r) => { r.hidden = r.dataset.run !== id; });
      active = runs.find((r) => r.dataset.run === id);
      at = still ? active.querySelectorAll(".events li").length - 1 : 0;
      play();
    };
    document.querySelectorAll("[data-tab]").forEach((t) => t.addEventListener("click", () => pick(t.dataset.tab)));
    if (still) runs.forEach((r) => show(r, r.querySelectorAll(".events li").length - 1));
    new IntersectionObserver(([e]) => { visible = e.isIntersecting; play(); }, { threshold: 0.25 })
      .observe(runs[0].parentElement);
    pick(runs[0].dataset.run);
  }

  // Search: an index of every section, fetched the first time it opens
  const box = document.querySelector("[data-search]");
  if (!box) return;
  const input = box.querySelector("[data-search-input]");
  const list = box.querySelector("[data-search-results]");
  let index = null;
  let picked = 0;

  const open = () => {
    box.hidden = false;
    input.focus();
    input.select();
    if (!index) fetch("/search.json").then((r) => r.json()).then((d) => { index = d; find(); }).catch(() => {});
  };
  const close = () => { box.hidden = true; };
  document.querySelectorAll("[data-search-open]").forEach((b) => b.addEventListener("click", open));
  box.addEventListener("click", (e) => { if (e.target === box) close(); });
  document.addEventListener("keydown", (e) => {
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName);
    if ((e.key === "/" && !typing) || (e.key === "k" && (e.metaKey || e.ctrlKey))) { e.preventDefault(); open(); }
    else if (e.key === "Escape" && !box.hidden) close();
  });

  const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const marked = (s, words) => {
    let out = esc(s);
    words.forEach((w) => { out = out.replace(new RegExp("(" + w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + ")", "gi"), "<mark>$1</mark>"); });
    return out;
  };
  const snippet = (text, words) => {
    const lower = text.toLowerCase();
    const at = Math.max(0, Math.min(...words.map((w) => { const i = lower.indexOf(w); return i < 0 ? Infinity : i; })) - 40);
    return (at > 0 ? "… " : "") + text.slice(isFinite(at) ? at : 0, (isFinite(at) ? at : 0) + 180);
  };

  const find = () => {
    const q = input.value.trim().toLowerCase();
    picked = 0;
    if (!q || !index) { list.innerHTML = ""; return; }
    const words = q.split(/\s+/).filter(Boolean);
    const scored = [];
    for (const e of index) {
      const page = e.p.toLowerCase(), head = (e.h || "").toLowerCase(), body = e.t.toLowerCase();
      let score = 0;
      let all = true;
      for (const w of words) {
        let s = 0;
        if (head.includes(w) || head.replace(/\s+/g, "").includes(w)) s += head.startsWith(w) || head.replace(/\s+/g, "") === w ? 12 : 8;
        if (page.includes(w)) s += 5;
        if (body.includes(w)) s += 1 + Math.min(3, body.split(w).length - 2);
        if (!s) { all = false; break; }
        score += s;
      }
      if (all) scored.push([score, e]);
    }
    scored.sort((a, b) => b[0] - a[0]);
    const top = scored.slice(0, 12);
    if (!top.length) { list.innerHTML = '<li class="none">Nothing found for “' + esc(q) + '”.</li>'; return; }
    list.innerHTML = top.map(([, e], i) =>
      '<li><a href="' + esc(e.u) + '"' + (i === 0 ? ' class="on"' : "") + ">" +
      '<div class="where">' + esc(e.p) + "</div>" +
      '<div class="what">' + marked(e.h || e.p, words) + "</div>" +
      (e.t ? '<div class="snip">' + marked(snippet(e.t, words), words) + "</div>" : "") +
      "</a></li>").join("");
  };
  input.addEventListener("input", find);
  input.addEventListener("keydown", (e) => {
    const links = [...list.querySelectorAll("a")];
    if (!links.length) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      picked = (picked + (e.key === "ArrowDown" ? 1 : links.length - 1)) % links.length;
      links.forEach((a, i) => a.classList.toggle("on", i === picked));
      links[picked].scrollIntoView({ block: "nearest" });
    } else if (e.key === "Enter") {
      e.preventDefault();
      location.href = links[picked].href;
      close();
    }
  });
})();
