// End-to-end check against a running deployment (local or Sprite).
//   BASE=https://<sprite>.sprites.app PIN=123456 node deploy/sprite/e2e.mjs
// Creates a throwaway event, exercises it through the public HTTP API, then deletes the event
// (which removes every photo it created). Existing events are only read, never changed.
const BASE = process.env.BASE?.replace(/\/$/, '');
const PIN = process.env.PIN;
if (!BASE || !PIN) {
  console.error('Set BASE and PIN');
  process.exit(2);
}

const results = [];
const check = (name, ok, detail = '') => {
  results.push({ name, ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  (${detail})` : ''}`);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Smallest useful JPEG: the server only sniffs the FF D8 FF signature.
const JPEG = Buffer.from(
  '/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////wgALCAABAAEBAREA/8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABPxA=',
  'base64',
);

let hostCookie = '';
async function admin(method, path, body) {
  return fetch(`${BASE}/api/admin${path}`, {
    method,
    headers: { Cookie: hostCookie, ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
}

// ---- SSE listener for one room ----
const events = [];
const sseAbort = new AbortController();
async function listen(room) {
  const res = await fetch(`${BASE}/api/rooms/${room}/events`, { signal: sseAbort.signal });
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += dec.decode(value, { stream: true });
    let i;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const frame = buf.slice(0, i);
      buf = buf.slice(i + 2);
      const ev = frame.match(/^event: (.+)$/m)?.[1];
      const data = frame.match(/^data: (.+)$/m)?.[1];
      if (ev) events.push({ ev, data: data ? JSON.parse(data).data : null, at: Date.now() });
    }
  }
}
async function waitFor(ev, pred = () => true, ms = 3000) {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    const hit = events.find((e) => e.ev === ev && pred(e.data));
    if (hit) return { hit, latency: hit.at - t0 };
    await sleep(50);
  }
  return null;
}

async function upload(room, token, caption, extraHeaders = {}) {
  const fd = new FormData();
  fd.append('photo', new Blob([JPEG], { type: 'image/jpeg' }), 'e2e.jpg');
  fd.append('nickname', 'e2e-bot');
  fd.append('caption', caption);
  const res = await fetch(`${BASE}/api/rooms/${room}/photos`, {
    method: 'POST',
    body: fd,
    headers: { ...(token ? { 'X-Device-Token': token } : {}), ...extraHeaders },
  });
  return { status: res.status, body: await res.json().catch(() => ({})) };
}

let room = '';
try {
  // Host login + throwaway event
  const login = await fetch(`${BASE}/api/admin/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ pin: PIN }),
  });
  const setCookie = login.headers.get('set-cookie') || '';
  hostCookie = setCookie.split(';')[0];
  check('admin login', login.status === 200, `${login.status}`);
  check('host cookie is HttpOnly + SameSite=Strict', /HttpOnly/.test(setCookie) && /SameSite=Strict/.test(setCookie));
  if (BASE.startsWith('https://')) check('host cookie is Secure over HTTPS', /Secure/.test(setCookie));

  const created = await admin('POST', '/events', { title: 'e2e check (safe to delete)', maxPhotosPerPerson: 5 });
  const ev = await created.json();
  room = ev.id;
  check('create event', created.status === 201 && /^[a-z0-9]{6}$/.test(room), room);

  listen(room).catch(() => {});

  // Pages
  for (const p of ['/', `/${room}`, `/${room}/wall`, '/admin', `/admin/${room}`]) {
    const r = await fetch(BASE + p);
    const html = await r.text();
    check(`GET ${p}`, r.status === 200 && html.includes('<div id="app">'), `${r.status}`);
  }
  const asset = (await (await fetch(BASE + '/')).text()).match(/\/assets\/[^"]+\.js/)?.[0];
  const ar = await fetch(BASE + asset);
  check('static asset cached for a year', /immutable/.test(ar.headers.get('cache-control') || ''), ar.headers.get('cache-control'));

  const cfg = await (await fetch(`${BASE}/api/rooms/${room}/config`)).json();
  check('room config', cfg.room === room && cfg.uploadsOpen === true);
  const missing = await fetch(`${BASE}/api/rooms/zzzzzz/config`);
  check('unknown room is 404', missing.status === 404);

  await sleep(800); // let the SSE stream connect

  // Uploads, quota, live broadcast, image bytes
  const created5 = [];
  let token = '';
  for (let i = 1; i <= 5; i++) {
    const r = await upload(room, token, `e2e photo ${i}`, { 'X-Forwarded-For': '9.9.9.9' });
    token = r.body.deviceToken || token;
    if (r.body.photo) created5.push(r.body.photo.id);
    if (i === 1) {
      const got = await waitFor('photo_added', (d) => d?.id === r.body.photo?.id);
      check('upload reaches the wall over SSE', !!got, got ? `${got.latency} ms after upload returned` : 'no event in 3 s');
    }
  }
  check('5 uploads accepted', created5.length === 5, `${created5.length}`);
  const sixth = await upload(room, token, 'e2e over limit');
  check('6th upload rejected', sixth.status === 429, `${sixth.status} ${sixth.body.code}`);

  const img = await fetch(`${BASE}/api/photos/${created5[0]}/image`);
  const bytes = Buffer.from(await img.arrayBuffer());
  check('image served back intact', img.status === 200 && bytes.equals(JPEG), `${img.status}`);

  await fetch(`${BASE}/api/rooms/${room}/reactions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ emoji: '🎉', nickname: 'e2e-bot' }),
  });
  check('reaction reaches the wall over SSE', !!(await waitFor('reaction', (d) => d?.nickname === 'e2e-bot')));

  // Moderation
  await admin('PUT', `/events/${room}`, { subtitle: 'e2e subtitle check' });
  check('settings change reaches the wall', !!(await waitFor('config_updated', (d) => d?.subtitle === 'e2e subtitle check')));

  const photos = (await (await admin('GET', `/events/${room}/photos`)).json()).photos;
  const mine = photos.find((p) => p.id === created5[0]);
  check('admin list includes device id', !!mine?.deviceId);

  await admin('PATCH', `/photos/${created5[1]}`, { hidden: true });
  check('hide removes it from the wall', !!(await waitFor('photo_deleted', (d) => d?.id === created5[1])));
  await admin('POST', `/photos/${created5[2]}/spotlight`);
  check('spotlight reaches the wall', !!(await waitFor('photo_spotlight', (d) => d?.id === created5[2])));

  const reset = await admin('POST', `/events/${room}/devices/${encodeURIComponent(mine.deviceId)}/reset`);
  check('reset phone limit', reset.status === 200, `${reset.status}`);
  const afterReset = await upload(room, token, 'e2e after reset');
  check('upload works again after reset', afterReset.status === 201, `${afterReset.status}`);

  await admin('PUT', `/events/${room}`, { uploadsOpen: false });
  const closed = await upload(room, '', 'e2e closed');
  check('closed event rejects uploads', closed.status === 403 && closed.body.code === 'UPLOADS_CLOSED', `${closed.status}`);

  const zip = await admin('GET', `/events/${room}/export.zip`);
  const zipBytes = Buffer.from(await zip.arrayBuffer());
  check('album ZIP downloads', zip.status === 200 && zipBytes.subarray(0, 2).toString() === 'PK', `${zipBytes.length} bytes`);
} catch (err) {
  check('unexpected error', false, String(err));
} finally {
  if (hostCookie && room) {
    const del = await admin('DELETE', `/events/${room}`);
    check('delete throwaway event (removes its photos)', del.status === 200, `${del.status}`);
    check('room_deleted reaches the wall', !!(await waitFor('room_deleted')));
    const gone = await fetch(`${BASE}/api/rooms/${room}/config`);
    check('deleted room is 404', gone.status === 404);
  }
  if (hostCookie) await fetch(`${BASE}/api/admin/logout`, { method: 'POST', headers: { Cookie: hostCookie } });
  sseAbort.abort();
  const failed = results.filter((r) => !r.ok).length;
  console.log(`\n${results.length - failed}/${results.length} checks passed`);
  process.exit(failed ? 1 : 0);
}
