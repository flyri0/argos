import { afterEach, beforeEach, describe, expect, it } from "vitest";

import i18n, { detectInitialLanguage, setLanguage } from "./i18n";

const LANGUAGE_KEY = "argos.language";

function setNavigatorLanguages(language: string, languages?: string[]): void {
  Object.defineProperty(navigator, "language", { value: language, configurable: true });
  Object.defineProperty(navigator, "languages", {
    value: languages ?? [language],
    configurable: true,
  });
}

describe("detectInitialLanguage", () => {
  const originalLanguage = navigator.language;
  const originalLanguages = navigator.languages;

  beforeEach(() => {
    localStorage.removeItem(LANGUAGE_KEY);
  });

  afterEach(() => {
    Object.defineProperty(navigator, "language", { value: originalLanguage, configurable: true });
    Object.defineProperty(navigator, "languages", {
      value: originalLanguages,
      configurable: true,
    });
    localStorage.removeItem(LANGUAGE_KEY);
  });

  it("prefers a stored manual override over the browser language (§4 settings override)", () => {
    localStorage.setItem(LANGUAGE_KEY, "en");
    setNavigatorLanguages("de-DE");
    expect(detectInitialLanguage()).toBe("en");
  });

  it("ignores an unsupported stored override and falls through to browser detection", () => {
    localStorage.setItem(LANGUAGE_KEY, "fr");
    setNavigatorLanguages("en-US");
    expect(detectInitialLanguage()).toBe("en");
  });

  it("matches a supported language by primary subtag (e.g. en-GB -> en)", () => {
    setNavigatorLanguages("en-GB");
    expect(detectInitialLanguage()).toBe("en");
  });

  it("falls back to English when no browser language is supported", () => {
    setNavigatorLanguages("fr-FR", ["fr-FR", "de-DE"]);
    expect(detectInitialLanguage()).toBe("en");
  });
});

describe("setLanguage", () => {
  afterEach(() => {
    localStorage.removeItem(LANGUAGE_KEY);
  });

  it("persists the manual override and switches the active language", () => {
    setLanguage("en");
    expect(localStorage.getItem(LANGUAGE_KEY)).toBe("en");
    expect(i18n.language).toBe("en");
  });
});
