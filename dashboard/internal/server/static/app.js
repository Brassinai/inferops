const state = {
  snapshot: null,
};

document.getElementById("refresh").addEventListener("click", () => {
  load().catch(showError);
});

async function load() {
  setHealth("Loading", "warn");
  const snapshot = await fetchJSON("/api/snapshot");
  state.snapshot = normalizeSnapshot(snapshot);
  render();
  setHealth("Ready", "ok");
}

async function fetchJSON(path) {
  const response = await fetch(path, { headers: { Accept: "application/json" } });
  if (!response.ok) {
    const body = await response.text();
    throw new Error(`${path} returned ${response.status}: ${body}`);
  }
  return response.json();
}

function normalizeSnapshot(snapshot) {
  const safe = snapshot || {};
  return {
    generatedAt: safe.generatedAt || "",
    namespace: safe.namespace || "unknown",
    summary: safe.summary || {},
    deployments: asArray(safe.deployments),
    caches: asArray(safe.caches),
    gpus: asArray(safe.gpus),
    events: asArray(safe.events),
  };
}

function render() {
  const { snapshot } = state;
  const activeDeployments = snapshot.deployments.filter((item) => item.phase === "Active").length;
  document.getElementById("subtitle").textContent =
    `${snapshot.namespace} namespace, refreshed ${formatTime(snapshot.generatedAt)}`;
  document.getElementById("deployment-count").textContent = snapshot.summary.deployments ?? snapshot.deployments.length;
  document.getElementById("active-count").textContent = activeDeployments;
  document.getElementById("cache-count").textContent = snapshot.summary.caches ?? snapshot.caches.length;

  renderDeployments(snapshot.deployments);
  renderCards("deployment-cards", snapshot.deployments, deploymentCard, "No deployments are visible in this namespace.");
  renderCards("caches", snapshot.caches, cacheCard, "No model caches are registered.");
  renderCards("gpus", snapshot.gpus, gpuCard, "No GPU resources are visible. CPU-only llama.cpp tests are expected to look like this.");
  renderCards("events", snapshot.events, eventCard, "No recent Kubernetes Events are visible.");
}

function renderDeployments(deployments) {
  const body = document.getElementById("deployments");
  body.replaceChildren();
  if (!deployments.length) {
    const row = document.createElement("tr");
    row.innerHTML = `<td colspan="6" class="empty-cell">No ModelDeployments found in this namespace.</td>`;
    body.append(row);
    return;
  }
  for (const deployment of deployments) {
    const row = document.createElement("tr");
    row.innerHTML = `
      <td>
        <strong>${escapeHTML(deployment.name)}</strong>
        <span class="subline">${escapeHTML(deployment.model)}</span>
      </td>
      <td>${pill(deployment.phase)}</td>
      <td>
        ${escapeHTML(deployment.runtime)}
        <span class="subline">${deployment.routing && deployment.routing.openAICompatible ? "OpenAI route" : "custom route"}</span>
      </td>
      <td>
        ${numberValue(deployment.scaling && deployment.scaling.readyReplicas)}/${numberValue((deployment.scaling && deployment.scaling.desiredReplicas) || (deployment.scaling && deployment.scaling.maxReplicas))}
        <span class="subline">${escapeHTML((deployment.scaling && deployment.scaling.reason) || "steady")}</span>
      </td>
      <td>
        ${pill((deployment.cache && deployment.cache.state) || "No cache")}
        <span class="subline">${gpuLine(deployment.gpu)}</span>
      </td>
      <td>
        ${endpointLink(deployment.endpoint)}
        <span class="subline">${escapeHTML((deployment.logs && deployment.logs.kubectl) || "")}</span>
      </td>
    `;
    body.append(row);
  }
}

function deploymentCard(item) {
  const conditions = asArray(item.conditions)
    .slice(0, 5)
    .map((condition) => `${condition.type}: ${condition.status}${condition.reason ? ` (${condition.reason})` : ""}`)
    .join("\n");
  return card(item.name, [
    ["Desired", (item.activation && item.activation.desiredState) || "Inactive"],
    ["Policy", (item.activation && item.activation.whenFull) || "Queue"],
    ["Route", (item.routing && item.routing.path) || (item.endpoint && item.endpoint.route) || ""],
    ["Service", (item.endpoint && item.endpoint.service) || ""],
    ["Cache", `${(item.cache && item.cache.state) || "-"} ${(item.cache && item.cache.nodeName) || ""}`.trim()],
    ["Conditions", conditions || "-"],
  ]);
}

function gpuCard(item) {
  return card(item.nodeName, [
    ["Resource", item.resource],
    ["Capacity", item.capacity],
    ["Allocatable", item.allocatable],
    ["Requested", item.requested],
  ]);
}

function cacheCard(item) {
  return card(item.name, [
    ["Phase", item.phase],
    ["Model", item.modelRepo],
    ["Revision", item.revision || ""],
    ["Node", item.nodeName || ""],
    ["Path", item.path || ""],
    ["Size", item.size || item.reservedSize || ""],
  ]);
}

function eventCard(item) {
  return card(`${item.reason || "Event"} ${item.count ? `(${item.count})` : ""}`, [
    ["Type", item.type],
    ["Object", item.involvedObject],
    ["Last seen", formatTime(item.lastSeen)],
    ["Message", item.message],
  ]);
}

function card(title, rows) {
  const article = document.createElement("article");
  const heading = document.createElement("h3");
  heading.textContent = title || "Unknown";
  const dl = document.createElement("dl");
  for (const [label, value] of rows) {
    const dt = document.createElement("dt");
    const dd = document.createElement("dd");
    dt.textContent = label;
    dd.textContent = value === undefined || value === null || value === "" ? "-" : String(value);
    dl.append(dt, dd);
  }
  article.append(heading, dl);
  return article;
}

function renderCards(id, items, renderer, emptyMessage) {
  const target = document.getElementById(id);
  target.replaceChildren();
  const list = asArray(items);
  if (!list.length) {
    target.append(emptyArticle(emptyMessage));
    return;
  }
  for (const item of list) {
    target.append(renderer(item));
  }
}

function emptyArticle(message) {
  const article = document.createElement("article");
  article.className = "empty-state";
  article.textContent = message || "No records visible.";
  return article;
}

function endpointLink(endpoint) {
  const safe = endpoint || {};
  const value = safe.gatewayUrl || safe.statusUrl || safe.route || "-";
  if (!safe.gatewayUrl) {
    return escapeHTML(value);
  }
  return `<a href="${escapeHTML(safe.gatewayUrl)}">${escapeHTML(value)}</a>`;
}

function gpuLine(gpu) {
  const safe = gpu || {};
  if (!safe.requestedCount) {
    return "CPU";
  }
  const pieces = [`${safe.requestedCount} GPU`];
  if (safe.vendor) pieces.push(safe.vendor);
  if (safe.type) pieces.push(safe.type);
  if (safe.assignedNode) pieces.push(`on ${safe.assignedNode}`);
  return escapeHTML(pieces.join(" "));
}

function pill(value) {
  const normalized = String(value || "Unknown");
  const cls = normalized === "Active" || normalized === "Ready"
    ? ""
    : normalized === "Failed" || normalized === "Unavailable"
      ? " bad"
      : " warn";
  return `<span class="pill${cls}">${escapeHTML(normalized)}</span>`;
}

function setHealth(label, mode) {
  const target = document.getElementById("health");
  target.textContent = label;
  target.className = `status-dot ${mode || ""}`.trim();
}

function showError(error) {
  setHealth("Error", "bad");
  document.getElementById("subtitle").textContent = error.message;
}

function asArray(value) {
  return Array.isArray(value) ? value : [];
}

function numberValue(value) {
  return Number.isFinite(Number(value)) ? Number(value) : 0;
}

function formatTime(value) {
  if (!value) return "unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]));
}

load().catch(showError);
