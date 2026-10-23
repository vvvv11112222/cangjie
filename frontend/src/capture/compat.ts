/** Explicit timeout/cancellation, including Safari without AbortSignal.any. */
export async function fetchWithDeadline(fetcher: typeof fetch, input: RequestInfo | URL, init: RequestInit,
  milliseconds: number, parent?: AbortSignal): Promise<Response> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  if (parent?.aborted) abort();
  else parent?.addEventListener('abort', abort, { once: true });
  const timer = setTimeout(abort, milliseconds);
  try { return await fetcher(input, { ...init, signal: controller.signal }); }
  finally { clearTimeout(timer); parent?.removeEventListener('abort', abort); }
}
