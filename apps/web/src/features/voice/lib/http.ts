export class ApiError extends Error {
  readonly status: number;
  readonly code: string | null;

  constructor(message: string, status: number, code: string | null = null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export async function readErrorMessage(
  response: Response,
  fallback: string,
): Promise<string> {
  try {
    const data = (await response.json()) as {
      detail?: string;
      error?: { code?: string; message?: string };
      message?: string;
    };

    if (typeof data.error?.message === "string" && data.error.message.trim()) {
      const code = data.error.code ? `[${data.error.code}] ` : "";
      return `${code}${data.error.message}`;
    }
    if (typeof data.detail === "string" && data.detail.trim()) {
      return data.detail;
    }
    if (typeof data.message === "string" && data.message.trim()) {
      return data.message;
    }
  } catch {
    // Ignore JSON parse failures.
  }

  return `${fallback} (${response.status})`;
}

export async function parseJson<T>(response: Response): Promise<T> {
  if (!response.ok) {
    throw new ApiError(
      await readErrorMessage(response, "请求失败"),
      response.status,
    );
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export function newIdempotencyKey(prefix = "lingow"): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}
