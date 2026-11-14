export function supportsGroupOpenAIFast(platform: string): boolean {
  return platform === "openai" || platform === "composite";
}

export function normalizeGroupOpenAIFast(
  platform: string,
  enabled: boolean,
): boolean {
  return supportsGroupOpenAIFast(platform) && enabled;
}

type OpenAIForcedSpeed = "force_openai_fast" | "force_openai_ultrafast";

export function setGroupOpenAIForcedSpeed(
  form: Record<OpenAIForcedSpeed, boolean>,
  field: OpenAIForcedSpeed,
  enabled: boolean,
): void {
  form[field] = enabled;
  if (enabled) {
    const other = field === "force_openai_fast"
      ? "force_openai_ultrafast"
      : "force_openai_fast";
    form[other] = false;
  }
}
