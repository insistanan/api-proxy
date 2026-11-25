export const DEFAULT_BRANCH_CONCURRENCY = 3

export function isPositiveInteger(value: unknown): boolean {
  return (typeof value === 'number' || typeof value === 'string') &&
    Number.isSafeInteger(Number(value)) && Number(value) > 0
}
