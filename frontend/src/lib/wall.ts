import type { Photo } from './api';

/** Height / width of a Polaroid card: 5cqw top border + 90cqw square photo + 28cqw caption strip. */
export const POLAROID_RATIO = 1.23;

/** FNV-1a hash so every photo gets the same tilt on every screen and every reload. */
export function hashString(value: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

/** Deterministic tilt in the range [-4deg, +4deg], never flatter than 0.6deg so cards read as hand-pinned. */
export function tiltFor(id: string): number {
  const unit = (hashString(id) % 10_000) / 10_000; // 0..1
  const tilt = unit * 8 - 4;
  if (Math.abs(tilt) < 0.6) return tilt < 0 ? -0.6 : 0.6;
  return Math.round(tilt * 100) / 100;
}

export function relativeTime(iso: string, now: number): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return '';
  const seconds = Math.max(0, Math.round((now - then) / 1000));
  if (seconds < 45) return 'just now';
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  return `${days}d ago`;
}

export function sortNewestFirst(photos: Photo[]): Photo[] {
  return [...photos].sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt));
}

export interface WallLayout {
  cols: number;
  rows: number;
  cardWidth: number;
  gap: number;
  capacity: number;
}

/**
 * Picks the largest card size that fits `count` Polaroids inside the wall area without scrolling.
 * When the wall is full, it falls back to the densest readable grid and reports how many cards fit.
 */
export function computeWallLayout(width: number, height: number, count: number): WallLayout {
  const gap = Math.round(Math.min(32, Math.max(14, width * 0.016)));
  if (width <= 0 || height <= 0) {
    return { cols: 4, rows: 3, cardWidth: 200, gap, capacity: Math.max(count, 12) };
  }

  const minCard = Math.max(120, Math.min(170, width / 11));
  const maxCard = Math.min(360, (height - gap) / POLAROID_RATIO);
  let best: WallLayout | null = null;

  for (let cols = 1; cols <= 14; cols++) {
    const fitWidth = (width - gap * (cols - 1)) / cols;
    const cardWidth = Math.min(maxCard, fitWidth);
    if (cols > 1 && fitWidth < minCard) break;
    const rows = Math.max(1, Math.floor((height + gap) / (cardWidth * POLAROID_RATIO + gap)));
    best = { cols, rows, cardWidth: Math.floor(cardWidth), gap, capacity: cols * rows };
    if (best.capacity >= count) return best;
  }

  return best ?? { cols: 1, rows: 1, cardWidth: Math.floor(minCard), gap, capacity: 1 };
}

export interface UploadTarget {
  url: string;
  displayUrl: string;
  hasEventCode: boolean;
  pointsToLocalhost: boolean;
}

const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '0.0.0.0', '[::1]', '::1']);

/**
 * Builds the guest upload URL for a room, encoded into the wall's QR code: `<origin>/<room>`.
 *  - `?url=` on the wall overrides the origin (useful when the projector laptop opens the wall via localhost).
 *  - `?code=` on the wall is forwarded so phones that scan the QR get the event code automatically.
 */
export function buildUploadTarget(location: Location, room: string): UploadTarget {
  const params = new URLSearchParams(location.search);
  const override = params.get('url')?.trim();
  const code = params.get('code')?.trim() || '';

  let base: URL;
  try {
    base = new URL(override || '/', location.origin);
  } catch {
    base = new URL('/', location.origin);
  }
  const target = new URL(`/${encodeURIComponent(room)}`, base.origin);
  if (code) target.searchParams.set('code', code);

  return {
    url: target.toString(),
    displayUrl: `${target.host}${target.pathname}${target.search}`,
    hasEventCode: code !== '',
    pointsToLocalhost: LOCAL_HOSTS.has(target.hostname),
  };
}
