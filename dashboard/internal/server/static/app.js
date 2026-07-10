const state = {
  snapshot: null,
  yaml: null,
};

document.getElementById("refresh").addEventListener("click", load);

async function load() {
  const [snapshot, yaml] = await Promise.all([
    fetchJSON("/api/snapshot"),
    fetchJSON("/api/generated-yaml"),
  ]);
  state.snapshot = snapshot;
  state.yaml = yaml;
  render();
}

async function fetchJSON(path) {
  const response = await fetch(path, { headers: { Accept: "application/json" } });
  if (!response.ok) {
    const body = await response.text();
    throw new Error(`${path} returned ${response.status}: ${body}`);
  }
  return response.json();
}

function render() {
  const { snapshot, yaml } = state;
  document.getElementById("subtitle").textContent =
    `${snapshot.namespace} namespace, refreshed ${snapshot.generatedAt}`;
  document.getElementById("deployment-count").textContent = snapshot.summary.deployments;
  document.getElementById("cache-count").textContent = snapshot.summary.caches;
  document.getElementById("runtime-count").textContent = snapshot.summary.runtimes;
  renderDeployments(snapshot.deployments);
  renderCards("gpus", snapshot.gpus, gpuCard);
  renderCards("caches", snapshot.caches, cacheCard);
  renderCards("events", snapshot.events, eventCard);
  document.getElementById("yaml").textContent = yaml.deployments.map((item) => item.yaml).join("\n---\n");
  renderMetrics(snapshot.metrics);
}

function renderDeployments(deployments) {
  const body = document.getElementById("deployments");
  body.replaceChildren();
  for (const deployment of deployments) {
    const row = document.createElement("tr");
    row.innerHTML = `
      <td><strong>${escapeHTML(deployment.name)}</strong><br><span class="muted">${escapeHTML(deployment.model)}</span></td>
      <td>${pill(deployment.phase)}</td>
      <td>${escapeHTML(deployment.runtime)}</td>
      <td>${escapeHTML(deployment.activation.desiredState || "Inactive")}<br><span class="muted">${escapeHTML(deployment.activation.whenFull || "Queue")}</span></td>
      <td>${deployment.scaling.readyReplicas}/${deployment.scaling.desiredReplicas || deployment.scaling.maxReplicas || 0}<br><span class="muted">${escapeHTML(deployment.scaling.reason || "")}</span></td>
      <td>${endpointLink(deployment.endpoint)}<br><span class="muted">${escapeHTML(deployment.logs.kubectl)}</span></td>
    `;
    body.append(row);
  }
}

function renderCards(id, items, renderer) {
  const target = document.getElementById(id);
  target.replaceChildren();
  if (!items.length) {
    const empty = document.createElement("article");
    empty.className = "muted";
    empty.textContent = "No records visible";
    target.append(empty);
    return;
  }
  for (const item of items) {
    target.append(renderer(item));
  }
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
  return card(`${item.reason} ${item.count ? `(${item.count})` : ""}`, [
    ["Type", item.type],
    ["Object", item.involvedObject],
    ["Last seen", item.lastSeen],
    ["Message", item.message],
  ]);
}

function card(title, rows) {
  const article = document.createElement("article");
  const heading = document.createElement("h3");
  heading.textContent = title;
  const dl = document.createElement("dl");
  for (const [label, value] of rows) {
    const dt = document.createElement("dt");
    const dd = document.createElement("dd");
    dt.textContent = label;
    dd.textContent = value || "-";
    dl.append(dt, dd);
  }
  article.append(heading, dl);
  return article;
}

function renderMetrics(metrics) {
  const target = document.getElementById("metrics");
  target.replaceChildren();
  for (const item of metrics.queries) {
    const wrapper = document.createElement("div");
    const name = document.createElement("strong");
    const query = document.createElement("code");
    name.textContent = item.name;
    query.textContent = item.query;
    wrapper.append(name, query);
    if (metrics.prometheusUrl) {
      const link = document.createElement("a");
      link.href = `${metrics.prometheusUrl}/graph?g0.expr=${encodeURIComponent(item.query)}`;
      link.textContent = "Open";
      link.target = "_blank";
      link.rel = "noreferrer";
      wrapper.append(link);
    }
    target.append(wrapper);
  }
}

function endpointLink(endpoint) {
  const value = endpoint.gatewayUrl || endpoint.statusUrl || endpoint.route || "-";
  if (!endpoint.gatewayUrl) {
    return escapeHTML(value);
  }
  return `<a href="${escapeHTML(endpoint.gatewayUrl)}">${escapeHTML(value)}</a>`;
}

function pill(value) {
  const normalized = String(value || "Unknown");
  const cls = normalized === "Active" || normalized === "Ready" ? "" :
    normalized === "Failed" || normalized === "Unavailable" ? " bad" : " warn";
  return `<span class="pill${cls}">${escapeHTML(normalized)}</span>`;
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

load().catch((error) => {
  document.getElementById("subtitle").textContent = error.message;
});
