"use strict";

const $ = (sel) => document.querySelector(sel);
const GIB = 1024 ** 3;

const PRESETS = {
  qq:      { host: "smtp.qq.com",        port: 465, security: "tls" },
  "163":   { host: "smtp.163.com",       port: 465, security: "tls" },
  exmail:  { host: "smtp.exmail.qq.com", port: 465, security: "tls" },
  aliyun:  { host: "smtp.mxhichina.com", port: 465, security: "tls" },
  gmail:   { host: "smtp.gmail.com",     port: 465, security: "tls" },
  outlook: { host: "smtp.office365.com", port: 587, security: "starttls" },
};

let state = null;   // /api/state
let cfg = null;     // /api/config

/* ---------- 工具 ---------- */

function fmtGB(v) { return (v / GIB).toFixed(1); }

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function fmtTime(iso) {
  const d = new Date(iso);
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

function toast(msg, kind = "") {
  const el = document.createElement("div");
  el.className = "toast " + kind;
  el.textContent = msg;
  $("#toasts").appendChild(el);
  setTimeout(() => el.remove(), 6000);
}

async function api(path, opts = {}) {
  const res = await fetch(path, { credentials: "same-origin", ...opts });
  if (!res.ok) {
    let msg = res.status + " " + res.statusText;
    try { msg = (await res.json()).error || msg; } catch (_) {}
    throw new Error(msg);
  }
  return res.json();
}

/* ---------- 仪表盘渲染 ---------- */

function driveColor(d) {
  if (d.alerting) return "var(--bad)";
  if (d.enabled && d.free_pct < d.threshold_pct * 2) return "var(--warn)";
  return "var(--ok)";
}

function donutSVG(d) {
  const R = 54, C = 2 * Math.PI * R, color = driveColor(d);
  const free = Math.min(100, Math.max(0, d.free_pct));
  const dash = (free / 100) * C;
  // 阈值刻度线：位于剩余弧上 thr% 处（从顶部顺时针）
  let tick = "";
  if (d.enabled && d.threshold_pct > 0) {
    const ang = (d.threshold_pct / 100) * 2 * Math.PI - Math.PI / 2;
    const x1 = 70 + Math.cos(ang) * (R - 10), y1 = 70 + Math.sin(ang) * (R - 10);
    const x2 = 70 + Math.cos(ang) * (R + 10), y2 = 70 + Math.sin(ang) * (R + 10);
    tick = `<line x1="${x1.toFixed(1)}" y1="${y1.toFixed(1)}" x2="${x2.toFixed(1)}" y2="${y2.toFixed(1)}" stroke="var(--text)" stroke-width="2.5" stroke-linecap="round"/>`;
  }
  return `
  <svg width="140" height="140" viewBox="0 0 140 140">
    <circle cx="70" cy="70" r="${R}" fill="none" stroke="var(--surface2)" stroke-width="13"/>
    <circle cx="70" cy="70" r="${R}" fill="none" stroke="${color}" stroke-width="13"
      stroke-linecap="round" stroke-dasharray="${dash.toFixed(1)} ${C.toFixed(1)}"
      transform="rotate(-90 70 70)"/>
    ${tick}
    <text class="donut-num" x="70" y="68" text-anchor="middle" fill="${color}">${d.free_pct.toFixed(0)}%</text>
    <text class="donut-cap" x="70" y="88" text-anchor="middle">剩余空间</text>
  </svg>`;
}

let lastDriveSig = "";

function renderDrives() {
  const grid = $("#drives");
  const drives = state?.drives || [];
  const sig = JSON.stringify(drives.map((d) => [d.id, d.free_pct.toFixed(2), d.alerting, d.enabled, d.threshold_pct, d.free, d.total]));
  if (sig === lastDriveSig) return;
  if (grid.contains(document.activeElement)) return; // 用户正在卡片上编辑，不打断
  lastDriveSig = sig;
  grid.innerHTML = "";
  $("#emptyDrives").classList.toggle("hidden", drives.length > 0);
  for (const d of drives) {
    const card = document.createElement("div");
    card.className = "drive-card" + (d.alerting ? " alerting" : "");
    card.innerHTML = `
      <div class="dc-head">
        <span class="dc-title">${d.id}</span>
        <span class="dc-label" title="${esc(d.label)}">${esc(d.label) || "固定磁盘"}</span>
      </div>
      <div class="donut-wrap">${donutSVG(d)}</div>
      <div class="dc-nums">
        <span>剩余 <b>${fmtGB(d.free)} GB</b></span>
        <span>共 <b>${fmtGB(d.total)} GB</b></span>
        <span>已用 <b>${d.used_pct.toFixed(1)}%</b></span>
      </div>
      <div class="dc-foot">
        <label class="thr-ctl">阈值
          <input type="number" min="1" max="50" step="1" value="${d.threshold_pct || 10}" data-thr="${d.id}" ${d.enabled ? "" : "disabled"}> %
        </label>
        <label class="switch" title="${d.enabled ? "监控中，点击停用" : "已停用，点击启用"}">
          <input type="checkbox" data-drive="${d.id}" ${d.enabled ? "checked" : ""}>
          <span class="slider"></span>
        </label>
      </div>`;
    grid.appendChild(card);
  }
  grid.querySelectorAll("input[data-drive]").forEach((el) =>
    el.addEventListener("change", () => saveDrives()));
  grid.querySelectorAll("input[data-thr]").forEach((el) =>
    el.addEventListener("change", () => saveDrives()));
}

function collectDrivesFromCards() {
  const list = [];
  const map = new Map((cfg.drives || []).map((d) => [d.id, d]));
  for (const d of state.drives) {
    const en = document.querySelector(`input[data-drive="${CSS.escape(d.id)}"]`);
    const thr = document.querySelector(`input[data-thr="${CSS.escape(d.id)}"]`);
    list.push({
      id: d.id,
      enabled: en ? en.checked : (map.get(d.id)?.enabled ?? false),
      threshold_pct: thr ? parseFloat(thr.value) || 10 : (map.get(d.id)?.threshold_pct ?? 10),
    });
  }
  return list;
}

async function saveDrives() {
  try {
    await api("/api/config", { method: "PUT", body: JSON.stringify({ drives: collectDrivesFromCards() }) });
    cfg = await api("/api/config");
    toast("磁盘监控设置已保存", "ok");
    refreshState();
  } catch (e) { toast("保存失败：" + e.message, "bad"); }
}

/* ---------- 状态头部 ---------- */

function renderStatus() {
  const pill = $("#statusPill");
  if (!state) return;
  if (state.paused) {
    pill.className = "pill pill-pause"; pill.textContent = "监控已暂停"; return;
  }
  const alerting = state.drives.filter((d) => d.alerting);
  const warn = state.drives.filter((d) => d.enabled && !d.alerting && d.free_pct < d.threshold_pct * 2);
  if (alerting.length) {
    pill.className = "pill pill-bad"; pill.textContent = `${alerting.length} 块磁盘告警`;
  } else if (warn.length) {
    pill.className = "pill pill-warn"; pill.textContent = "接近阈值";
  } else {
    pill.className = "pill pill-ok"; pill.textContent = "运行正常";
  }
}

/* ---------- 历史 ---------- */

const KIND_TAG = { alert: ["告警", "tag-alert"], recovery: ["恢复", "tag-recovery"], error: ["失败", "tag-error"], info: ["信息", "tag-info"] };

async function loadEvents() {
  const evs = await api("/api/events");
  const body = $("#eventsBody");
  if (!evs.length) {
    body.innerHTML = `<tr><td colspan="4" class="muted" style="text-align:center;padding:28px">暂无记录</td></tr>`;
    return;
  }
  body.innerHTML = evs.map((e) => {
    const [label, cls] = KIND_TAG[e.kind] || KIND_TAG.info;
    return `<tr><td style="white-space:nowrap">${fmtTime(e.time)}</td><td><span class="tag ${cls}">${label}</span></td><td>${esc(e.drive) || "—"}</td><td>${esc(e.message)}</td></tr>`;
  }).join("");
}

/* ---------- 设置 ---------- */

function fillSettings() {
  $("#inInterval").value = cfg.check_interval_sec;
  $("#inCooldown").value = cfg.cooldown_sec;
  $("#inHyst").value = cfg.recovery_hysteresis_pct;
  $("#inNotify").checked = cfg.notify_on_recovery;
  const s = cfg.smtp;
  $("#inPreset").value = s.preset || "custom";
  $("#inHost").value = s.host || "";
  $("#inPort").value = s.port || "";
  $("#inSecurity").value = s.security || "tls";
  $("#inUser").value = s.user || "";
  $("#inAlias").value = s.from_alias || "";
  $("#inRecipients").value = (s.recipients || []).join(", ");
  $("#passHint").textContent = s.has_pass ? "已保存授权码（留空则不修改）" : "尚未配置";
  $("#inAutostart").checked = !!cfg.autostart;
  $("#inMinimized").checked = !!cfg.launch_minimized;
}

function buildPayload() {
  const recipients = $("#inRecipients").value.split(/[,，;；\s]+/).filter(Boolean);
  const p = {
    check_interval_sec: parseInt($("#inInterval").value) || 300,
    cooldown_sec: parseInt($("#inCooldown").value) || 21600,
    recovery_hysteresis_pct: parseFloat($("#inHyst").value) || 2,
    notify_on_recovery: $("#inNotify").checked,
    smtp: {
      preset: $("#inPreset").value,
      host: $("#inHost").value.trim(),
      port: parseInt($("#inPort").value) || 0,
      security: $("#inSecurity").value,
      user: $("#inUser").value.trim(),
      pass: $("#inPass").value,
      from_alias: $("#inAlias").value.trim(),
      recipients,
    },
    autostart: $("#inAutostart").checked,
    launch_minimized: $("#inMinimized").checked,
  };
  if (state && state.drives) p.drives = collectDrivesFromCards();
  return p;
}

async function saveSettings() {
  const msg = $("#saveMsg");
  try {
    await api("/api/config", { method: "PUT", body: JSON.stringify(buildPayload()) });
    cfg = await api("/api/config");
    $("#inPass").value = "";
    fillSettings();
    msg.textContent = "已保存 " + new Date().toLocaleTimeString();
    toast("设置已保存", "ok");
    refreshState();
  } catch (e) {
    msg.textContent = "";
    toast("保存失败：" + e.message, "bad");
  }
}

async function testSMTP() {
  const btn = $("#smtpTestBtn");
  btn.disabled = true; btn.textContent = "发送中…";
  try {
    // 先保存再测试，保证服务器端参数一致
    await api("/api/config", { method: "PUT", body: JSON.stringify(buildPayload()) });
    cfg = await api("/api/config");
    await api("/api/smtp/test", { method: "POST" });
    toast("测试邮件已发送，请查收", "ok");
  } catch (e) {
    toast("发送失败：" + e.message, "bad");
  } finally {
    btn.disabled = false; btn.textContent = "发送测试邮件";
  }
}

/* ---------- 数据刷新与事件流 ---------- */

async function refreshState() {
  try {
    state = await api("/api/state");
    $("#hostline").textContent = state.hostname + (state.paused ? " · 已暂停" : "");
    $("#version").textContent = "v" + state.version;
    renderStatus();
    renderDrives();
  } catch (e) { /* 服务重启中，忽略 */ }
}

function listenStream() {
  const es = new EventSource("/api/stream");
  es.addEventListener("alert", (m) => { const e = JSON.parse(m.data); toast("磁盘告警：" + e.message, "bad"); });
  es.addEventListener("recovery", (m) => { const e = JSON.parse(m.data); toast(e.message, "ok"); });
  es.addEventListener("error", (m) => { try { const e = JSON.parse(m.data); toast(e.message, "warn"); } catch (_) {} });
}

/* ---------- 交互装配 ---------- */

document.querySelectorAll(".tab").forEach((t) =>
  t.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((x) => x.classList.remove("active"));
    document.querySelectorAll(".panel").forEach((x) => x.classList.remove("active"));
    t.classList.add("active");
    $("#tab-" + t.dataset.tab).classList.add("active");
    if (t.dataset.tab === "history") loadEvents();
  }));

$("#applyAllBtn").addEventListener("click", () => {
  const v = parseFloat($("#applyAllPct").value);
  if (!v || v < 1 || v > 50) { toast("请输入 1-50 的阈值", "warn"); return; }
  document.querySelectorAll("#drives input[data-thr]").forEach((el) => { el.value = v; });
  saveDrives();
});

$("#checkNowBtn").addEventListener("click", async () => {
  try { await api("/api/check", { method: "POST" }); toast("已执行检查", "ok"); setTimeout(refreshState, 800); }
  catch (e) { toast("检查失败：" + e.message, "bad"); }
});

$("#inPreset").addEventListener("change", () => {
  const p = PRESETS[$("#inPreset").value];
  if (p) { $("#inHost").value = p.host; $("#inPort").value = p.port; $("#inSecurity").value = p.security; }
});

$("#saveBtn").addEventListener("click", saveSettings);
$("#smtpTestBtn").addEventListener("click", testSMTP);
$("#refreshEventsBtn").addEventListener("click", loadEvents);

(async function init() {
  try {
    cfg = await api("/api/config");
    fillSettings();
  } catch (e) {
    if (e.message.startsWith("401")) { toast("请先通过托盘「打开控制台」访问本页面", "bad"); return; }
  }
  await refreshState();
  setInterval(refreshState, 3000);
  listenStream();
})();
