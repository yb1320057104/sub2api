import { describe, expect, it } from "vitest";

import {
  normalizeGroupOpenAIFast,
  setGroupOpenAIForcedSpeed,
  supportsGroupOpenAIFast,
} from "../groupsOpenAIFast";
import en from "@/i18n/locales/en/admin/overview";
import zh from "@/i18n/locales/zh/admin/overview";

describe("groupsOpenAIFast", () => {
  it("supports OpenAI and composite groups", () => {
    expect(supportsGroupOpenAIFast("openai")).toBe(true);
    expect(supportsGroupOpenAIFast("composite")).toBe(true);
    expect(supportsGroupOpenAIFast("anthropic")).toBe(false);
  });

  it("clears stale enabled state on unsupported platforms", () => {
    expect(normalizeGroupOpenAIFast("openai", true)).toBe(true);
    expect(normalizeGroupOpenAIFast("composite", true)).toBe(true);
    expect(normalizeGroupOpenAIFast("anthropic", true)).toBe(false);
    expect(normalizeGroupOpenAIFast("openai", false)).toBe(false);
  });

  it("provides localized switch labels", () => {
    expect(zh.groups.openaiFast).toMatchObject({
      title: expect.any(String),
      force: expect.any(String),
      hint: expect.stringContaining("service_tier=priority"),
      forceUltrafast: "强制使用 Ultrafast",
      ultrafastBadge: expect.any(String),
      combinedHint: expect.stringContaining("其他模型仍使用 Fast"),
      free: expect.any(String),
      freeHint: expect.stringContaining("Standard"),
      freeUltrafast: "免费 Ultrafast",
      freeUltrafastBadge: expect.any(String),
      freeUltrafastHint: expect.stringContaining("并非请求全免"),
      policyHint: expect.stringContaining("两者独立"),
    });
    expect(en.groups.openaiFast).toMatchObject({
      title: expect.any(String),
      force: expect.any(String),
      hint: expect.stringContaining("service_tier=priority"),
      forceUltrafast: "Force Ultrafast",
      ultrafastBadge: expect.any(String),
      combinedHint: expect.stringContaining("other models still use Fast"),
      free: expect.any(String),
      freeHint: expect.stringContaining("Standard"),
      freeUltrafast: "Free Ultrafast",
      freeUltrafastBadge: expect.any(String),
      freeUltrafastHint: expect.stringContaining("the request is not free"),
      policyHint: expect.stringContaining("independently"),
    });
    for (const locale of [zh, en]) {
      expect(locale.groups.openaiFast.ultrafastHint).toContain("gpt-6-astra");
      expect(locale.groups.openaiFast.ultrafastHint).toContain("gpt-6.1-sol");
      expect(locale.groups.openaiFast.ultrafastHint).toContain("Pro500");
    }
    expect(zh.groups.openaiFast.ultrafastHint).toContain("不保证固定倍速");
    expect(en.groups.openaiFast.ultrafastHint).toContain("no fixed speed increase guaranteed");
    expect(zh.groups.openaiFast.ultrafastHint).toContain("平台定价配置");
    expect(en.groups.openaiFast.ultrafastHint).toContain("platform pricing configuration");
    expect(zh.groups.openaiFast.freeUltrafastHint).toContain("Standard 模型价及分组等既有倍率");
    expect(en.groups.openaiFast.freeUltrafastHint).toContain("Standard model prices and existing group and other multipliers");
    expect(zh.groups.openaiFast.freeUltrafastHint).toContain("不减少上游额度消耗");
    expect(en.groups.openaiFast.freeUltrafastHint).toContain("reduce upstream quota consumption");
    for (const locale of [zh, en]) {
      expect(locale.groups.openaiFast.ultrafastHint).not.toMatch(/8\s*(?:倍|×|x)/);
    }
  });

  it("enables only the selected speed without changing either free tier", () => {
    const form = {
      force_openai_fast: true,
      force_openai_ultrafast: false,
      free_openai_fast: true,
      free_openai_ultrafast: true,
    };
    setGroupOpenAIForcedSpeed(form, "force_openai_ultrafast", true);
    expect(form).toEqual({
      force_openai_fast: false,
      force_openai_ultrafast: true,
      free_openai_fast: true,
      free_openai_ultrafast: true,
    });
    setGroupOpenAIForcedSpeed(form, "force_openai_fast", true);
    expect(form).toEqual({
      force_openai_fast: true,
      force_openai_ultrafast: false,
      free_openai_fast: true,
      free_openai_ultrafast: true,
    });
  });

  it("allows disabling either saved policy without changing the other", () => {
    const form = { force_openai_fast: true, force_openai_ultrafast: true };
    setGroupOpenAIForcedSpeed(form, "force_openai_fast", false);
    expect(form).toEqual({ force_openai_fast: false, force_openai_ultrafast: true });
    setGroupOpenAIForcedSpeed(form, "force_openai_ultrafast", false);
    expect(form).toEqual({ force_openai_fast: false, force_openai_ultrafast: false });
  });
});
