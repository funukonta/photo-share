export interface Photo {
  id: string;
  eventId?: string;
  contentType: string;
  nickname: string;
  caption: string;
  deviceId?: string;
  hidden?: boolean;
  imageUrl: string;
  createdAt: string;
}

export interface PublicConfigResponse {
  room: string;
  title: string;
  subtitle: string;
  maxPhotosPerPerson: number;
  requiresEventCode: boolean;
  uploadsOpen: boolean;
}

export interface MeResponse {
  deviceId: string;
  deviceToken: string;
  uploadedCount: number;
  remainingCount: number;
  maxPhotosPerPerson: number;
  photos: Photo[];
}

export interface ListPhotosResponse {
  photos: Photo[];
  total: number;
}

export interface UploadPhotoResponse {
  photo: Photo;
  deviceToken: string;
  uploadedCount: number;
  remainingCount: number;
  maxPhotosPerPerson: number;
}

export interface ReactionPayload {
  id: string;
  emoji: string;
  nickname: string;
  createdAt: string;
}

export class ApiError extends Error {
  status: number;
  code?: string;
  data?: Record<string, unknown>;

  constructor(message: string, status: number, code?: string, data?: Record<string, unknown>) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.data = data;
  }
}

/** True when the error means "this room code does not exist" (unknown, mistyped, or deleted event). */
export function isRoomNotFound(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404 && err.code === 'ROOM_NOT_FOUND';
}

const STORAGE_KEYS = {
  DEVICE_TOKEN: 'ps_device_token',
  NICKNAME: 'ps_nickname',
  EVENT_CODE_PREFIX: 'ps_event_code:',
} as const;

export const ALLOWED_REACTION_EMOJIS = ['🎉', '🔥', '🥹', '❤️', '🚀', '🐛', '👏', '✨'] as const;
export type ReactionEmoji = (typeof ALLOWED_REACTION_EMOJIS)[number];

/** Room codes are 6 lowercase letters/digits. Returns the normalized code, or '' if it can't be one. */
export function normalizeRoomCode(raw: string): string {
  const code = raw.trim().toLowerCase();
  return /^[a-z0-9]{6}$/.test(code) ? code : '';
}

function roomPath(room: string, path: string): string {
  return `/api/rooms/${encodeURIComponent(room)}${path}`;
}

export function getStoredDeviceToken(): string {
  try {
    return localStorage.getItem(STORAGE_KEYS.DEVICE_TOKEN) || '';
  } catch {
    return '';
  }
}

export function setStoredDeviceToken(token: string): void {
  if (!token) return;
  try {
    localStorage.setItem(STORAGE_KEYS.DEVICE_TOKEN, token);
  } catch {
    // Ignore private-mode storage errors
  }
}

export function getStoredNickname(): string {
  try {
    return localStorage.getItem(STORAGE_KEYS.NICKNAME) || '';
  } catch {
    return '';
  }
}

export function setStoredNickname(nickname: string): void {
  try {
    localStorage.setItem(STORAGE_KEYS.NICKNAME, nickname);
  } catch {
    // Ignore storage errors
  }
}

/** Event codes are per room. A ?code=... in the URL (from the wall's QR) wins and is remembered. */
export function getStoredEventCode(room: string): string {
  const key = STORAGE_KEYS.EVENT_CODE_PREFIX + room;
  try {
    if (typeof window !== 'undefined') {
      const urlCode = new URLSearchParams(window.location.search).get('code')?.trim();
      if (urlCode) {
        localStorage.setItem(key, urlCode);
        return urlCode;
      }
    }
    return localStorage.getItem(key) || '';
  } catch {
    return '';
  }
}

export function setStoredEventCode(room: string, code: string): void {
  try {
    localStorage.setItem(STORAGE_KEYS.EVENT_CODE_PREFIX + room, code.trim());
  } catch {
    // Ignore storage errors
  }
}

function buildHeaders(room: string | null, extra?: Record<string, string>): HeadersInit {
  const headers: Record<string, string> = { ...(extra || {}) };

  const token = getStoredDeviceToken();
  if (token) {
    headers['X-Device-Token'] = token;
  }

  if (room) {
    const eventCode = getStoredEventCode(room);
    if (eventCode) {
      headers['X-Event-Code'] = eventCode;
    }
  }

  return headers;
}

async function handleResponse<T>(res: Response): Promise<T> {
  const headerToken = res.headers.get('X-Device-Token');
  if (headerToken) {
    setStoredDeviceToken(headerToken);
  }

  let data: any = null;
  const contentType = res.headers.get('content-type') || '';
  if (contentType.includes('application/json')) {
    try {
      data = await res.json();
    } catch {
      data = null;
    }
  }

  if (!res.ok) {
    const message = data?.error || `Request failed with status ${res.status}`;
    throw new ApiError(message, res.status, data?.code, data);
  }

  if (data && typeof data === 'object' && typeof data.deviceToken === 'string' && data.deviceToken) {
    setStoredDeviceToken(data.deviceToken);
  }

  return data as T;
}

async function roomGet<T>(room: string, path: string): Promise<T> {
  const res = await fetch(roomPath(room, path), {
    method: 'GET',
    credentials: 'include',
    headers: buildHeaders(room),
  });
  return handleResponse<T>(res);
}

export function fetchConfig(room: string): Promise<PublicConfigResponse> {
  return roomGet(room, '/config');
}

export function fetchMe(room: string): Promise<MeResponse> {
  return roomGet(room, '/me');
}

export function fetchPhotos(room: string): Promise<ListPhotosResponse> {
  return roomGet(room, '/photos');
}

export function roomEventsUrl(room: string): string {
  return roomPath(room, '/events');
}

export interface UploadPhotoInput {
  file: File | Blob;
  filename?: string;
  nickname: string;
  caption: string;
  eventCode?: string;
}

export async function uploadPhoto(room: string, input: UploadPhotoInput): Promise<UploadPhotoResponse> {
  const formData = new FormData();
  const filename = input.filename || (input.file instanceof File ? input.file.name : 'memory.jpg');

  formData.append('photo', input.file, filename);
  formData.append('nickname', input.nickname.trim());
  formData.append('caption', input.caption.trim());

  const code = (input.eventCode ?? getStoredEventCode(room)).trim();
  if (code) {
    formData.append('event_code', code);
  }

  const res = await fetch(roomPath(room, '/photos'), {
    method: 'POST',
    credentials: 'include',
    headers: buildHeaders(room, code ? { 'X-Event-Code': code } : undefined),
    body: formData,
  });

  return handleResponse<UploadPhotoResponse>(res);
}

export async function sendReaction(room: string, emoji: string, nickname?: string): Promise<ReactionPayload> {
  const res = await fetch(roomPath(room, '/reactions'), {
    method: 'POST',
    credentials: 'include',
    headers: buildHeaders(room, { 'Content-Type': 'application/json' }),
    body: JSON.stringify({
      emoji,
      nickname: (nickname ?? getStoredNickname()).trim(),
    }),
  });

  return handleResponse<ReactionPayload>(res);
}

// ---------------------------------------------------------------------------
// Host admin API (`/api/admin/*`). Auth is an HttpOnly `ps_host` session cookie set by `adminLogin`.
// ---------------------------------------------------------------------------

export interface AdminEvent {
  id: string;
  title: string;
  subtitle: string;
  eventCode: string;
  maxPhotosPerPerson: number;
  maxPhotosPerIp: number;
  uploadsOpen: boolean;
  createdAt: string;
  photoCount: number;
  hiddenCount: number;
}

export type AdminEventInput = Partial<
  Pick<AdminEvent, 'title' | 'subtitle' | 'eventCode' | 'maxPhotosPerPerson' | 'maxPhotosPerIp' | 'uploadsOpen'>
>;

export interface AdminPhotosResponse {
  photos: Photo[];
  total: number;
}

async function adminFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {};
  if (init.body) headers['Content-Type'] = 'application/json';
  const res = await fetch(path, { ...init, credentials: 'include', headers });
  return handleResponse<T>(res);
}

const adminEventPath = (room: string, path = '') => `/api/admin/events/${encodeURIComponent(room)}${path}`;

export function adminExportUrl(room: string): string {
  return adminEventPath(room, '/export.zip');
}

export function adminLogin(pin: string): Promise<{ ok: boolean; expiresAt: string }> {
  return adminFetch('/api/admin/login', { method: 'POST', body: JSON.stringify({ pin }) });
}

export function adminLogout(): Promise<{ ok: boolean }> {
  return adminFetch('/api/admin/logout', { method: 'POST' });
}

/** Resolves true when the browser already holds a valid host session. */
export async function adminHasSession(): Promise<boolean> {
  try {
    await adminFetch('/api/admin/session');
    return true;
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return false;
    throw err;
  }
}

export function fetchAdminEvents(): Promise<{ events: AdminEvent[] }> {
  return adminFetch('/api/admin/events');
}

export function createAdminEvent(input: AdminEventInput): Promise<AdminEvent> {
  return adminFetch('/api/admin/events', { method: 'POST', body: JSON.stringify(input) });
}

export function fetchAdminEvent(room: string): Promise<AdminEvent> {
  return adminFetch(adminEventPath(room));
}

export function updateAdminEvent(room: string, patch: AdminEventInput): Promise<AdminEvent> {
  return adminFetch(adminEventPath(room), { method: 'PUT', body: JSON.stringify(patch) });
}

export function deleteAdminEvent(room: string): Promise<{ deleted: string; photosDeleted: number }> {
  return adminFetch(adminEventPath(room), { method: 'DELETE' });
}

export function fetchAdminPhotos(room: string): Promise<AdminPhotosResponse> {
  return adminFetch(adminEventPath(room, '/photos'));
}

export function setPhotoHidden(id: string, hidden: boolean): Promise<{ photo: Photo }> {
  return adminFetch(`/api/admin/photos/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify({ hidden }),
  });
}

export function deleteAdminPhoto(id: string): Promise<{ deleted: string }> {
  return adminFetch(`/api/admin/photos/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export function spotlightPhoto(id: string): Promise<{ spotlighted: string }> {
  return adminFetch(`/api/admin/photos/${encodeURIComponent(id)}/spotlight`, { method: 'POST' });
}

export function resetDeviceQuota(room: string, deviceId: string): Promise<{ deviceId: string; photosCounted: number }> {
  return adminFetch(adminEventPath(room, `/devices/${encodeURIComponent(deviceId)}/reset`), { method: 'POST' });
}
