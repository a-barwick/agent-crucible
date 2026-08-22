(() => {
  const $ = (id) => document.getElementById(id);

  const state = {
    meta: null,
    sweep: null,
    p: 0,
    selected: 0,
    enabled: new Set(),
  };

  const heatWord = (p) => {
    if (p <= 0) return "cold";
    if (p < 8) return "warm";
    if (p < 18) return "forging";
    if (p < 26) return "white-hot";
    return "melt";
  };

  async function loadMeta() {
    const res = await fetch("/api/meta");
    state.meta = await res.json();
    $("agent-name").textContent = state.meta.agent.name;
    $("agent-task").textContent = "Close Acme · email the AE";
    const defaults = state.meta.defaults.faults || [];
    state.enabled = new Set(defaults);
    renderFaults();
    drawGraph(state.meta.agent.graph.nodes, [], []);
  }

  function renderFaults() {
    const row = $("fault-row");
    row.innerHTML = "";
    for (const f of state.meta.faults) {
      const lab = document.createElement("label");
      if (f.mvp) lab.classList.add("mvp");
      const box = document.createElement("input");
      box.type = "checkbox";
      box.checked = state.enabled.has(f.type);
      box.addEventListener("change", () => {
        if (box.checked) state.enabled.add(f.type);
        else state.enabled.delete(f.type);
        lab.classList.toggle("on", box.checked);
        sweep();
      });
      lab.classList.toggle("on", box.checked);
      lab.title = f.blurb;
      lab.append(box, document.createTextNode(f.label));
      row.append(lab);
    }
  }

  function currentSuite() {
    if (!state.sweep) return null;
    const want = Math.round(state.p);
    let best = state.sweep.suites[0];
    let dist = 99;
    for (const s of state.sweep.suites) {
      const sp = Math.round((s.config.p || 0) * 100);
      const d = Math.abs(sp - want);
      if (d < dist) {
        dist = d;
        best = s;
      }
    }
    return best;
  }

  function renderSuite() {
    const suite = currentSuite();
    if (!suite) return;
    const pct = Math.round(suite.survival * 100);
    const el = $("survival");
    el.textContent = pct + "%";
    el.className = "giant " + (pct >= 70 ? "" : pct >= 40 ? "hot" : "dead");
    $("survival-sub").textContent =
      `${suite.trials.length} trials · seed ${suite.config.seed} · p=${Math.round(suite.config.p * 100)}%`;
    const order = ["completed", "recovered", "aborted", "failed"];
    $("counts").innerHTML = order
      .map((k) => {
        const n = suite.counts[k] || 0;
        return `<li><span><i class="dot ${k}"></i> ${k}</span><span>${n}</span></li>`;
      })
      .join("");

    const tiles = $("tiles");
    tiles.innerHTML = "";
    suite.trials.forEach((t) => {
      const b = document.createElement("button");
      b.className = "tile " + t.outcome + (t.n === state.selected ? " sel" : "");
      b.title = `trial ${t.n}: ${t.outcome}${t.faults?.length ? " · " + t.faults.join("+") : ""}`;
      b.addEventListener("click", () => {
        state.selected = t.n;
        renderSuite();
      });
      tiles.append(b);
    });

    const curve = $("curve");
    if (curve && state.sweep) {
      const want = Math.round(state.p);
      curve.innerHTML = state.sweep.suites
        .map((s) => {
          const sp = Math.round((s.config.p || 0) * 100);
          const h = Math.max(2, Math.round((s.survival || 0) * 48));
          const on = sp === want ? "on" : "";
          return `<i class="${on}" style="height:${h}px" title="${sp}% → ${Math.round(s.survival * 100)}%"></i>`;
        })
        .join("");
    }

    $("clusters").innerHTML = suite.clusters
      .slice(0, 8)
      .map((c) => {
        return `<li data-sample="${c.sample_trial}"><span>${esc(c.id)}</span><span>${c.n} · ${Math.round(c.rate * 100)}%</span></li>`;
      })
      .join("");
    $("clusters").querySelectorAll("li").forEach((li) => {
      li.addEventListener("click", () => {
        state.selected = Number(li.dataset.sample);
        renderSuite();
      });
    });

    const c = suite.critique;
    $("headline").textContent = c.headline;
    $("paragraphs").innerHTML = (c.paragraphs || []).map((p) => `<p>${esc(p)}</p>`).join("");
    $("fixes").innerHTML = (c.fixes || [])
      .map((f) => `<li><code>${esc(f.node)}</code> — ${esc(f.advice)}</li>`)
      .join("");

    const trial = suite.trials.find((t) => t.n === state.selected) || suite.trials[0];
    if (trial) renderTrial(suite, trial);

    const heat = Math.min(1, state.p / 30);
    document.documentElement.style.setProperty("--heat", String(0.08 + heat * 0.55));
  }

  function renderTrial(suite, trial) {
    $("tl-title").textContent = `Trial ${trial.n} · ${trial.outcome}`;
    $("tl-reason").textContent = trial.reason;
    $("replay-cmd").textContent =
      `crucible replay -seed ${suite.config.seed} -trial ${trial.n} -p ${suite.config.p}`;

    const hit = new Set();
    const faulted = new Set();
    for (const ev of trial.events || []) {
      if (ev.kind === "node_enter" && ev.node) hit.add(ev.node);
      if (ev.kind === "fault") {
        if (ev.node) faulted.add(ev.node);
        if (ev.tool) faulted.add(ev.tool);
      }
    }
    const nodes = state.meta?.agent?.graph?.nodes || [];
    drawGraph(nodes, hit, faulted);

    const tl = $("timeline");
    tl.innerHTML = (trial.events || [])
      .map((ev) => {
        const cls = ev.kind === "fault" ? "fault" : ev.kind;
        return `<li class="${cls}"><span>${ev.tick}</span><span class="kind">${esc(ev.kind)}</span><span>${esc(ev.message)}</span></li>`;
      })
      .join("");
  }

  function drawGraph(nodes, hit, faulted) {
    $("graph").innerHTML = nodes
      .filter((n) => n !== "end" && n !== "abort")
      .map((n) => {
        const cls = faulted.has(n) ? "faulted" : hit.has(n) ? "hit" : "";
        return `<li class="${cls}">${n}</li>`;
      })
      .join("");
  }

  function payload() {
    return {
      seed: Number($("seed").value) || 42,
      trials: Number($("trials").value) || 40,
      faults: [...state.enabled],
      max_p: 0.3,
      step: 0.01,
    };
  }

  async function sweep() {
    $("survival-sub").textContent = "pouring…";
    const res = await fetch("/api/sweep", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload()),
    });
    state.sweep = await res.json();
    if (state.selected >= (Number($("trials").value) || 40)) state.selected = 0;
    renderSuite();
  }

  function onSlider() {
    state.p = Number($("p-slider").value);
    $("p-value").textContent = state.p + "%";
    $("p-hint").textContent = heatWord(state.p);
    renderSuite();
  }

  function esc(s) {
    return String(s ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;");
  }

  $("p-slider").addEventListener("input", onSlider);
  $("resweep").addEventListener("click", sweep);
  $("seed").addEventListener("change", sweep);
  $("trials").addEventListener("change", sweep);

  loadMeta().then(sweep).catch((err) => {
    $("headline").textContent = "Could not reach the runner: " + err.message;
  });
})();
