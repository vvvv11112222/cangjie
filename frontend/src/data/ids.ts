/**
 * 业务 ID 的校验与规范化。
 *
 * 为什么需要：课堂详情的 id 来自地址栏（`#/sessions/{id}`），用户可能把它写成大写十六进制，
 * 也可能粘贴非法编码。协议里 UUID 只是"字符串"，大小写不同但含义相同（评审意见 F10），
 * 所以比较前统一规范成小写；非法输入要在路由层拦住，不能让它一路走到 React 根节点（F09）。
 */

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(value: string): boolean {
  return UUID_PATTERN.test(value);
}

/** UUID 按值比较，不区分大小写；非 UUID 原样返回，交给调用方决定怎么处理。 */
export function normalizeId(value: string): string {
  return isUuid(value) ? value.toLowerCase() : value;
}

/** 两个业务 ID 是否指向同一个资源（UUID 大小写不敏感）。 */
export function sameId(left: string, right: string): boolean {
  return normalizeId(left) === normalizeId(right);
}
