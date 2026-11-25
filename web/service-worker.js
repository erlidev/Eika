const cacheName = "eika-shell-__VERSION__";
const shell = __SHELL__;
const shellPaths = new Set(shell);

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(cacheName)
      .then((cache) => cache.addAll(shell.map((path) => new Request(path, { cache: "reload" })))),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) =>
        Promise.all(
          names
            .filter((name) => name.startsWith("eika-shell-") && name !== cacheName)
            .map((name) => caches.delete(name)),
        ),
      ),
  );
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (
    request.method !== "GET" ||
    url.origin !== self.location.origin ||
    url.pathname === "/api" ||
    url.pathname.startsWith("/api/")
  )
    return;

  if (request.mode === "navigate") {
    event.respondWith(
      fetch(request).catch(() => caches.open(cacheName).then((cache) => cache.match("/"))),
    );
  } else if (!url.search && shellPaths.has(url.pathname)) {
    event.respondWith(
      caches
        .open(cacheName)
        .then((cache) => cache.match(url.pathname))
        .then((response) => response || fetch(request)),
    );
  }
});
