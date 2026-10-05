/**
 * 读取固定 JSON 样例时的最小运行期守卫。
 * 目的是在样例与协议漂移时给出可定位的错误，而不是让页面静默渲染 undefined。
 * 完整结构、字段全集和业务语义仍以 `python tools/check_docs.py` 为准。
 */

export function guardFail(path: string, detail: string): never {
  throw new Error(`固定样例不符合协议：${path} ${detail}`);
}

export function asRecord(value: unknown, path: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    guardFail(path, '应为对象');
  }
  return value as Record<string, unknown>;
}

export function field(source: Record<string, unknown>, key: string, path: string): unknown {
  const value = source[key];
  if (value === undefined) {
    guardFail(`${path}.${key}`, '缺少字段');
  }
  return value;
}

export function asString(source: Record<string, unknown>, key: string, path: string): string {
  const value = field(source, key, path);
  if (typeof value !== 'string') {
    guardFail(`${path}.${key}`, '应为字符串');
  }
  return value;
}

export function asNullableString(
  source: Record<string, unknown>,
  key: string,
  path: string,
): string | null {
  const value = field(source, key, path);
  if (value === null) {
    return null;
  }
  if (typeof value !== 'string') {
    guardFail(`${path}.${key}`, '应为字符串或 null');
  }
  return value;
}

export function asInteger(source: Record<string, unknown>, key: string, path: string): number {
  const value = field(source, key, path);
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    guardFail(`${path}.${key}`, '应为整数');
  }
  return value;
}

export function asBoolean(source: Record<string, unknown>, key: string, path: string): boolean {
  const value = field(source, key, path);
  if (typeof value !== 'boolean') {
    guardFail(`${path}.${key}`, '应为布尔值');
  }
  return value;
}

export function asArray(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) {
    guardFail(path, '应为数组');
  }
  return value;
}

export function asEnum<T extends string>(
  source: Record<string, unknown>,
  key: string,
  path: string,
  allowed: readonly T[],
): T {
  const value = field(source, key, path);
  if (typeof value !== 'string' || !allowed.includes(value as T)) {
    guardFail(`${path}.${key}`, `应为 ${allowed.join(' / ')}，实际为 ${JSON.stringify(value)}`);
  }
  return value as T;
}

export function enumList<T extends string>(
  source: Record<string, unknown>,
  key: string,
  path: string,
  allowed: readonly T[],
): T[] {
  return asArray(field(source, key, path), `${path}.${key}`).map((item, index) => {
    if (typeof item !== 'string' || !allowed.includes(item as T)) {
      guardFail(`${path}.${key}[${index}]`, `应为 ${allowed.join(' / ')}`);
    }
    return item as T;
  });
}

export function stringList(source: Record<string, unknown>, key: string, path: string): string[] {
  return asArray(field(source, key, path), `${path}.${key}`).map((item, index) => {
    if (typeof item !== 'string') {
      guardFail(`${path}.${key}[${index}]`, '应为字符串');
    }
    return item;
  });
}

export function objectList(
  source: Record<string, unknown>,
  key: string,
  path: string,
): Record<string, unknown>[] {
  return asArray(field(source, key, path), `${path}.${key}`).map((item, index) =>
    asRecord(item, `${path}.${key}[${index}]`),
  );
}
