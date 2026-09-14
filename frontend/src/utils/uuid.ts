/**
 * 生成 UUID v4
 */
export function generateUUID(): string {
  return crypto.randomUUID()
}
