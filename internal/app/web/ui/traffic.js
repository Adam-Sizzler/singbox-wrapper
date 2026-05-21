(function () {
  function asNumber(value) {
    var parsed = Number(value);
    if (!isFinite(parsed) || parsed < 0) return 0;
    return parsed;
  }

  function formatBytes(value) {
    var bytes = asNumber(value);
    var units = ["B", "kB", "MB", "GB", "TB"];
    var unitIndex = 0;
    while (bytes >= 1000 && unitIndex < units.length - 1) {
      bytes = bytes / 1000;
      unitIndex++;
    }
    var precision = unitIndex === 0 ? 0 : (bytes >= 10 ? 0 : 1);
    return bytes.toFixed(precision) + " " + units[unitIndex];
  }

  function formatSpeed(value) {
    var bits = asNumber(value) * 8;
    var units = ["bit/s", "kbit/s", "Mbit/s", "Gbit/s", "Tbit/s"];
    var unitIndex = 0;
    while (bits >= 1000 && unitIndex < units.length - 1) {
      bits = bits / 1000;
      unitIndex++;
    }
    var precision = unitIndex === 0 ? 0 : (bits >= 10 ? 0 : 1);
    return bits.toFixed(precision) + " " + units[unitIndex];
  }

  function setText(root, selector, value) {
    var node = root ? root.querySelector(selector) : null;
    if (node) node.textContent = value;
  }

  function iconSVG(kind) {
    var path = kind === "upload"
      ? '<path d="M200,112H56l72-72Z" opacity="0.2"></path><path d="M205.66,106.34l-72-72a8,8,0,0,0-11.32,0l-72,72A8,8,0,0,0,56,120h64v96a8,8,0,0,0,16,0V120h64a8,8,0,0,0,5.66-13.66ZM75.31,104,128,51.31,180.69,104Z"></path>'
      : '<path d="M200,144l-72,72L56,144Z" opacity="0.2"></path><path d="M207.39,140.94A8,8,0,0,0,200,136H136V40a8,8,0,0,0-16,0v96H56a8,8,0,0,0-5.66,13.66l72,72a8,8,0,0,0,11.32,0l72-72A8,8,0,0,0,207.39,140.94ZM128,204.69,75.31,152H180.69Z"></path>';
    return '<svg class="traffic-icon" viewBox="0 0 256 256" aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg" fill="currentColor" stroke="currentColor" stroke-width="0">' + path + '</svg>';
  }

  function metricHTML(kind) {
    return '' +
      '<div class="traffic-metric traffic-' + kind + '">' +
        iconSVG(kind) +
        '<div class="traffic-label" data-field="' + kind + '-label"></div>' +
        '<div class="traffic-speed" data-field="' + kind + '-speed">0 B/s</div>' +
        '<div class="traffic-total" data-field="' + kind + '-total">0 B</div>' +
      '</div>';
  }

  function ensureMarkup(root) {
    if (!root || root.getAttribute("data-ready") === "true") return;
    root.innerHTML = metricHTML("upload") + metricHTML("download");
    root.setAttribute("data-ready", "true");
  }

  function render(root, traffic, running, labels) {
    if (!root) return;
    ensureMarkup(root);
    var data = traffic || {};
    if (!running || data.available === false) {
      root.hidden = true;
      return;
    }

    root.hidden = false;
    setText(root, '[data-field="upload-label"]', labels && labels.upload ? labels.upload : "Upload");
    setText(root, '[data-field="download-label"]', labels && labels.download ? labels.download : "Download");
    setText(root, '[data-field="upload-speed"]', formatSpeed(data.upload_speed));
    setText(root, '[data-field="download-speed"]', formatSpeed(data.download_speed));
    setText(root, '[data-field="upload-total"]', formatBytes(data.upload_total));
    setText(root, '[data-field="download-total"]', formatBytes(data.download_total));
  }

  window.SBTrafficUI = {
    render: render,
    formatBytes: formatBytes,
    formatSpeed: formatSpeed
  };
})();
