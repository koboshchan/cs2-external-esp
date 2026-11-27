/**
 * Per-connection token bucket. Cheap and predictable: a host pushing 30 fps
 * never hits it, a hostile client spamming frames gets cut off.
 */
export class RateLimiter {
  private tokens: number;
  private last: number;

  constructor(
    private readonly capacity: number,
    private readonly refillPerSecond: number,
    now = Date.now(),
  ) {
    this.tokens = capacity;
    this.last = now;
  }

  /** Returns true when the action is allowed. */
  take(cost = 1, now = Date.now()): boolean {
    const elapsed = Math.max(0, now - this.last) / 1000;
    this.last = now;
    this.tokens = Math.min(this.capacity, this.tokens + elapsed * this.refillPerSecond);

    if (this.tokens < cost) return false;
    this.tokens -= cost;
    return true;
  }
}
