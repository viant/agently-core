// Goja deliberately has no native Intl; the following official polyfills own it.
if (typeof globalThis.Intl === "undefined") globalThis.Intl = {};
