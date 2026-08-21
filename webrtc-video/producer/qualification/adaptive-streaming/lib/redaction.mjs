const queryCredential =
  /((?:rstream(?:\.|%2e)token)(?:=|%3d))([^&#\s"'<>()[\]{}]+)/giu;
const bearerCredential = /(\bBearer\s+)[A-Za-z0-9._~+/=-]{8,}/giu;
const jsonWebToken =
  /eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}(?:\.[A-Za-z0-9_-]{10,})?/gu;

export function redactSensitiveText(value) {
  return String(value)
    .replace(queryCredential, "$1[REDACTED]")
    .replace(bearerCredential, "$1[REDACTED]")
    .replace(jsonWebToken, "[REDACTED]");
}

export function redactError(value) {
  const source = value instanceof Error ? value : new Error(String(value));
  const error = new Error(redactSensitiveText(source.message));
  if (source.stack) {
    error.stack = redactSensitiveText(source.stack);
  }
  return error;
}

export function containsSensitiveText(value) {
  const source = String(value);
  return redactSensitiveText(source) !== source;
}
