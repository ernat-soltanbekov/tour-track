"use strict";

// Progressive enhancement: the same form remains a normal GET without JS.
// All API text enters through textContent, never through HTML interpolation.
(() => {
  const fallbackImages = () =>
    document.querySelectorAll("img").forEach((img) => {
      const fallback = () => {
        img.src = "/static/artist.svg";
      };
      img.addEventListener("error", fallback, { once: true });
      if (img.complete && img.naturalWidth === 0) fallback();
    });
  fallbackImages();
  const form = document.querySelector("#filters");
  if (!form || !window.fetch || !window.AbortController) return;

  const grid = document.querySelector("#artist-grid");
  const status = document.querySelector("#result-status");
  const errorBox = document.querySelector("#request-error");
  let controller;
  let sequence = 0;
  let debounce;

  function element(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function card(artist) {
    const article = element("article", "artist-card");
    const link = element("a", "card-link");
    link.href = `/artists/${artist.id}`;
    link.setAttribute("aria-label", `Explore ${artist.name}`);
    const picture = element("div", "card-image");
    const img = element("img");
    img.src = artist.image;
    img.alt = artist.name;
    img.width = 400;
    img.height = 400;
    img.loading = "lazy";
    const arrow = element("span", "card-arrow", "↗");
    arrow.setAttribute("aria-hidden", "true");
    picture.append(
      img,
      element(
        "span",
        "card-index",
        `ARTIST / ${String(artist.id).padStart(2, "0")}`,
      ),
      arrow,
    );
    const content = element("div", "card-content");
    const meta = element("div", "card-meta");
    meta.append(
      element("span", "", `EST. ${artist.creationDate}`),
      element("span", `badge ${artist.label.toLowerCase()}`, artist.label),
    );
    if (artist.risingStar)
      meta.append(element("span", "badge rising", "↗ Rising Star"));
    const stats = element("div", "card-stats");
    [
      [artist.tourSpread, "countries"],
      [artist.concertCount, "dates"],
      [artist.score, "score"],
    ].forEach(([value, label]) => {
      const item = element("span", label === "score" ? "score-mini" : "");
      item.append(
        element("strong", "", value),
        document.createTextNode(` ${label}`),
      );
      stats.append(item);
    });
    content.append(meta, element("h3", "", artist.name), stats);
    link.append(picture, content);
    article.append(link);
    return article;
  }

  function render(data) {
    const fragment = document.createDocumentFragment();
    data.artists.forEach((artist) => fragment.append(card(artist)));
    if (!data.artists.length) {
      const empty = element("div", "empty-state");
      const reset = element("a", "button", "Clear filters");
      reset.href = "/";
      empty.append(
        element("span", "", "⌕"),
        element("h3", "", "No matching artists."),
        element("p", "", "Try another name, country, or activity level."),
        reset,
      );
      fragment.append(empty);
    }
    grid.replaceChildren(fragment);
    status.textContent = `${data.matched} of ${data.total} artists`;
    document.querySelector("#stat-artists").textContent = data.total;
    document.querySelector("#stat-countries").textContent =
      data.countries.length;
    document.querySelector("#stat-dates").textContent = data.concertCount;
    const freshness = document.querySelector("#source-line");
    freshness.replaceChildren(
      element("span", data.stale ? "status-dot stale-dot" : "status-dot"),
      document.createTextNode(
        data.stale
          ? "Cached archive · source temporarily unavailable or updating"
          : "Archive connected",
      ),
      element(
        "span",
        "source-time",
        `Retrieved ${new Date(data.fetchedAt).toISOString().replace("T", " ").slice(0, 16)} UTC`,
      ),
    );
    fallbackImages();
  }

  async function update(pushHistory = true) {
    clearTimeout(debounce);
    if (!form.reportValidity()) return;
    if (controller) controller.abort();
    controller = new AbortController();
    const currentController = controller;
    const current = ++sequence;
    const timer = setTimeout(() => currentController.abort(), 12000);
    const params = new URLSearchParams(new FormData(form));
    for (const [key, value] of [...params])
      if (!value || (key === "sort" && value === "name")) params.delete(key);
    errorBox.hidden = true;
    status.textContent = "Finding your frequency…";
    grid.setAttribute("aria-busy", "true");
    try {
      const response = await fetch(`/api/artists?${params}`, {
        signal: controller.signal,
        headers: { Accept: "application/json" },
      });
      if (!response.ok)
        throw new Error(
          `The server returned ${response.status}. Please retry.`,
        );
      const data = await response.json();
      if (current !== sequence) return;
      render(data);
      if (pushHistory)
        history.pushState(null, "", params.size ? `/?${params}` : "/");
    } catch (error) {
      if (current !== sequence) return;
      errorBox.textContent =
        error.name === "AbortError"
          ? "The request took too long. Your previous results are still here. Press Explore to retry."
          : `${error.message} Your previous results are still here.`;
      errorBox.hidden = false;
      status.textContent = "Results could not be updated.";
    } finally {
      clearTimeout(timer);
      if (current === sequence) grid.removeAttribute("aria-busy");
    }
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    update();
  });
  form
    .querySelectorAll("select")
    .forEach((select) => select.addEventListener("change", () => update()));
  form.querySelector("input").addEventListener("input", () => {
    clearTimeout(debounce);
    // Invalidate the previous request immediately, before the debounce elapses.
    sequence++;
    if (controller) controller.abort();
    grid.removeAttribute("aria-busy");
    debounce = setTimeout(() => update(), 280);
  });
  window.addEventListener("popstate", () => {
    const params = new URLSearchParams(location.search);
    for (const name of ["q", "label", "country", "sort"])
      form.elements.namedItem(name).value =
        params.get(name) || (name === "sort" ? "name" : "");
    update(false);
  });
})();
