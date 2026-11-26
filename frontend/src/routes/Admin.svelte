<script lang="ts">
  import { onMount } from 'svelte';
  import {
    ApiError,
    adminExportUrl,
    deleteAdminEvent,
    deleteAdminPhoto,
    fetchAdminEvent,
    fetchAdminPhotos,
    resetDeviceQuota,
    setPhotoHidden,
    spotlightPhoto,
    updateAdminEvent,
    type AdminEvent,
    type AdminEventInput,
    type Photo,
  } from '../lib/api';
  import { connectSSE, type SSEClient } from '../lib/sse';
  import { relativeTime } from '../lib/wall';
  import { useHostSession } from '../components/HostGate.svelte';
  import {
    ArrowLeft,
    Check,
    Copy,
    Download,
    Eye,
    EyeOff,
    LoaderCircle,
    LogOut,
    RotateCcw,
    Trash2,
    Tv,
    ExternalLink,
  } from 'lucide-svelte';

  let { room }: { room: string } = $props();
  const host = useHostSession();

  type Filter = 'all' | 'visible' | 'hidden';

  let now = $state(Date.now());
  let notFound = $state(false);
  let copied = $state(false);
  let togglingUploads = $state(false);

  // ---- Settings ----
  let config = $state<AdminEvent | null>(null);
  let fTitle = $state('');
  let fSubtitle = $state('');
  let fMax = $state(5);
  let fCode = $state('');
  let fieldErrors = $state<Record<string, string>>({});
  let saving = $state(false);
  let saveMessage = $state<{ tone: 'ok' | 'error'; text: string } | null>(null);

  // ---- Photos ----
  let photos = $state<Photo[]>([]);
  let photosState = $state<'loading' | 'ready' | 'error'>('loading');
  let photosError = $state('');
  let filter = $state<Filter>('all');
  let busy = $state<Record<string, boolean>>({});
  let connected = $state(false);

  // ---- Feedback ----
  let toast = $state<{ tone: 'ok' | 'error'; text: string } | null>(null);
  let toastTimer: ReturnType<typeof setTimeout> | null = null;

  interface ConfirmState {
    title: string;
    body: string;
    confirmLabel: string;
    photo?: Photo;
    danger?: boolean;
    run: () => Promise<void>;
  }
  let confirmState = $state<ConfirmState | null>(null);
  let confirmBusy = $state(false);
  let dialogEl = $state<HTMLDialogElement | null>(null);

  let sse: SSEClient | null = null;
  let refreshTimer: ReturnType<typeof setTimeout> | null = null;

  let visibleCount = $derived(photos.filter((p) => !p.hidden).length);
  let hiddenCount = $derived(photos.length - visibleCount);
  let filtered = $derived(
    filter === 'visible' ? photos.filter((p) => !p.hidden) : filter === 'hidden' ? photos.filter((p) => p.hidden) : photos,
  );
  /** Uploads that still count against each phone's limit (hidden photos do not count, same rule as the upload API). */
  let deviceUsage = $derived.by(() => {
    const usage = new Map<string, number>();
    for (const p of photos) {
      if (p.deviceId && !p.hidden) usage.set(p.deviceId, (usage.get(p.deviceId) ?? 0) + 1);
    }
    return usage;
  });

  let dirty = $derived(
    !!config &&
      (fTitle.trim() !== config.title ||
        fSubtitle.trim() !== config.subtitle ||
        Number(fMax) !== config.maxPhotosPerPerson ||
        fCode.trim() !== config.eventCode),
  );
  let wallHref = $derived(config?.eventCode ? `/${room}/wall?code=${encodeURIComponent(config.eventCode)}` : `/${room}/wall`);
  let guestHref = $derived(`/${room}`);
  let guestUrl = $derived(typeof window !== 'undefined' ? `${window.location.origin}/${room}` : `/${room}`);

  function showToast(text: string, tone: 'ok' | 'error' = 'ok') {
    toast = { text, tone };
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (toast = null), 4000);
  }

  function errorText(err: unknown, fallback: string): string {
    if (err instanceof ApiError) return err.message;
    if (err instanceof TypeError) return 'The server could not be reached. Check the connection and try again.';
    return fallback;
  }

  /** Sends the host back to the PIN screen when the session cookie expired. Returns true if it did. */
  function handleAuthError(err: unknown): boolean {
    if (err instanceof ApiError && err.status === 401) {
      stopLive();
      host.expire();
      return true;
    }
    if (err instanceof ApiError && err.status === 404 && err.code === 'ROOM_NOT_FOUND') {
      stopLive();
      notFound = true;
      return true;
    }
    return false;
  }

  function fillForm(c: AdminEvent) {
    fTitle = c.title;
    fSubtitle = c.subtitle;
    fMax = c.maxPhotosPerPerson;
    fCode = c.eventCode;
    fieldErrors = {};
  }

  async function loadConfig() {
    const c = await fetchAdminEvent(room);
    config = c;
    fillForm(c);
  }

  async function loadPhotos() {
    try {
      const res = await fetchAdminPhotos(room);
      photos = res.photos || [];
      photosState = 'ready';
      photosError = '';
    } catch (err) {
      if (handleAuthError(err)) return;
      if (photosState !== 'ready') photosState = 'error';
      photosError = errorText(err, 'The photo list could not be loaded.');
    }
  }

  async function enterPanel() {
    photosState = 'loading';
    try {
      await Promise.all([loadConfig(), loadPhotos()]);
    } catch (err) {
      if (handleAuthError(err)) return;
      showToast(errorText(err, 'Settings could not be loaded.'), 'error');
    }
    if (!notFound) startLive();
  }

  function scheduleRefresh() {
    if (refreshTimer) clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      refreshTimer = null;
      loadPhotos();
    }, 400);
  }

  function startLive() {
    stopLive();
    let hadDisconnect = false;
    sse = connectSSE(room, {
      onConnectionChange: (isConn) => {
        if (isConn && hadDisconnect) {
          hadDisconnect = false;
          loadPhotos();
        }
        if (!isConn && connected) hadDisconnect = true;
        connected = isConn;
      },
      // The public events carry no device IDs, so refetch the admin list instead of patching it.
      onPhotoAdded: scheduleRefresh,
      onPhotoDeleted: scheduleRefresh,
      onRoomDeleted: () => {
        stopLive();
        notFound = true;
      },
    });
  }

  function stopLive() {
    sse?.close();
    sse = null;
    connected = false;
  }

  async function logout() {
    stopLive();
    await host.logout();
  }

  async function copyGuestLink() {
    try {
      await navigator.clipboard.writeText(guestUrl);
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } catch {
      showToast(`Copy failed. The guest link is ${guestUrl}`, 'error');
    }
  }

  async function toggleUploads() {
    if (!config || togglingUploads) return;
    togglingUploads = true;
    const open = !config.uploadsOpen;
    try {
      config = await updateAdminEvent(room, { uploadsOpen: open });
      showToast(open ? 'Uploads are open again.' : 'Uploads are closed. The wall stays up and reactions still work.');
    } catch (err) {
      if (!handleAuthError(err)) showToast(errorText(err, 'That did not work. Try again.'), 'error');
    } finally {
      togglingUploads = false;
    }
  }

  function askDeleteEvent() {
    if (!config) return;
    const count = config.photoCount + config.hiddenCount;
    openConfirm({
      title: `Delete "${config.title}"?`,
      body: `This removes the event and all ${count} ${count === 1 ? 'photo' : 'photos'} in it. Open walls and phones show "event not found". Download the album first if you want to keep the photos. This cannot be undone.`,
      confirmLabel: 'Delete event',
      danger: true,
      run: async () => {
        await deleteAdminEvent(room);
        stopLive();
        window.location.assign('/admin');
      },
    });
  }

  // ---- Settings ----
  async function saveSettings(e: SubmitEvent) {
    e.preventDefault();
    if (!config || !dirty || saving) return;
    saving = true;
    saveMessage = null;
    fieldErrors = {};

    const patch: AdminEventInput = {};
    if (fTitle.trim() !== config.title) patch.title = fTitle.trim();
    if (fSubtitle.trim() !== config.subtitle) patch.subtitle = fSubtitle.trim();
    if (Number(fMax) !== config.maxPhotosPerPerson) patch.maxPhotosPerPerson = Number(fMax);
    const codeChanged = fCode.trim() !== config.eventCode;
    if (codeChanged) patch.eventCode = fCode.trim();

    try {
      const updated = await updateAdminEvent(room, patch);
      config = updated;
      fillForm(updated);
      saveMessage = {
        tone: 'ok',
        text:
          codeChanged && updated.eventCode
            ? 'Saved. The event code changed, so reopen the wall with the new link below to update its QR code.'
            : codeChanged
              ? 'Saved. The event code is off, so anyone with the link can upload now.'
              : 'Saved. The wall and phones show the new settings now.',
      };
    } catch (err) {
      if (handleAuthError(err)) return;
      if (err instanceof ApiError && err.data && typeof err.data.fields === 'object' && err.data.fields) {
        fieldErrors = err.data.fields as Record<string, string>;
        saveMessage = { tone: 'error', text: 'Some settings need fixing before they can be saved.' };
      } else {
        saveMessage = { tone: 'error', text: errorText(err, 'Settings could not be saved.') };
      }
    } finally {
      saving = false;
    }
  }

  // ---- Photo actions ----
  async function withBusy(id: string, fn: () => Promise<void>) {
    if (busy[id]) return;
    busy = { ...busy, [id]: true };
    try {
      await fn();
    } catch (err) {
      if (!handleAuthError(err)) showToast(errorText(err, 'That did not work. Try again.'), 'error');
    } finally {
      const { [id]: _, ...rest } = busy;
      busy = rest;
    }
  }

  function toggleHidden(photo: Photo) {
    const hide = !photo.hidden;
    withBusy(photo.id, async () => {
      const res = await setPhotoHidden(photo.id, hide);
      photos = photos.map((p) => (p.id === photo.id ? { ...p, hidden: res.photo.hidden } : p));
      showToast(hide ? `Hid @${photo.nickname}'s photo from the wall.` : `@${photo.nickname}'s photo is back on the wall.`);
    });
  }

  function sendToSpotlight(photo: Photo) {
    withBusy(photo.id, async () => {
      await spotlightPhoto(photo.id);
      showToast(`@${photo.nickname}'s photo is next in the wall spotlight.`);
    });
  }

  function openConfirm(state: ConfirmState) {
    confirmState = state;
    queueMicrotask(() => dialogEl?.showModal());
  }

  function askDelete(photo: Photo) {
    openConfirm({
      title: 'Delete this photo for good?',
      body: 'It disappears from the wall, from the phone that sent it, and from the album download. This cannot be undone. To take it off the wall but keep it, use Hide instead.',
      confirmLabel: 'Delete photo',
      photo,
      run: async () => {
        await deleteAdminPhoto(photo.id);
        photos = photos.filter((p) => p.id !== photo.id);
        showToast(`Deleted @${photo.nickname}'s photo.`);
      },
    });
  }

  function askReset(photo: Photo) {
    const deviceId = photo.deviceId;
    if (!deviceId) return;
    const used = deviceUsage.get(deviceId) ?? 0;
    openConfirm({
      title: `Give @${photo.nickname}'s phone a fresh limit?`,
      body: `This phone has used ${used} of ${config?.maxPhotosPerPerson ?? 5} uploads. After the reset it can send ${config?.maxPhotosPerPerson ?? 5} more. Its photos stay on the wall, but they no longer show in that phone's own "Your shared memories" list.`,
      confirmLabel: 'Reset limit',
      run: async () => {
        await resetDeviceQuota(room, deviceId);
        await loadPhotos();
        showToast(`@${photo.nickname}'s phone can upload again.`);
      },
    });
  }

  async function runConfirm() {
    if (!confirmState || confirmBusy) return;
    confirmBusy = true;
    try {
      await confirmState.run();
      dialogEl?.close();
    } catch (err) {
      dialogEl?.close();
      if (!handleAuthError(err)) showToast(errorText(err, 'That did not work. Try again.'), 'error');
    } finally {
      confirmBusy = false;
    }
  }

  onMount(() => {
    enterPanel();

    const clock = setInterval(() => (now = Date.now()), 1000);
    return () => {
      clearInterval(clock);
      stopLive();
      if (refreshTimer) clearTimeout(refreshTimer);
      if (toastTimer) clearTimeout(toastTimer);
    };
  });
</script>

{#if notFound}
  <main class="min-h-dvh darkroom-bg flex items-center justify-center p-6">
    <div class="w-full max-w-sm rounded-xl border border-white/10 bg-stone-900/80 p-6 text-center" role="alert">
      <h1 class="font-display text-xl font-bold">Event not found</h1>
      <p class="mt-2 text-sm text-stone-300">There is no event with room code <code class="text-[#faf6f0]">{room}</code>. It may have been deleted.</p>
      <a href="/admin" class="btn-primary mt-5">All events</a>
    </div>
  </main>
{:else}
  <div class="min-h-dvh darkroom-bg text-[#faf6f0]">
    <header class="border-b border-white/10">
      <div class="max-w-7xl mx-auto px-4 sm:px-6 py-4 flex flex-wrap items-center justify-between gap-3">
        <div class="min-w-0">
          <a href="/admin" class="inline-flex min-h-11 items-center gap-1.5 text-sm text-stone-300 hover:text-[#faf6f0]">
            <ArrowLeft class="w-4 h-4" aria-hidden="true" />
            <span>All events</span>
          </a>
          <h1 class="font-display text-xl sm:text-2xl font-bold truncate">{config?.title ?? 'Loading event'}</h1>
          <p class="text-sm text-stone-300">
            Room <code class="font-semibold text-amber-300">{room}</code>
            <span class="text-stone-400">
              · {connected ? 'live updates on' : 'reconnecting to live updates'}
            </span>
          </p>
        </div>
        <nav class="flex flex-wrap gap-2" aria-label="Host actions">
          <a href={wallHref} target="_blank" rel="noopener" class="btn-ghost">
            <Tv class="w-4 h-4" aria-hidden="true" />
            <span>Open the wall</span>
            <ExternalLink class="w-3.5 h-3.5 opacity-70" aria-hidden="true" />
            <span class="sr-only">(opens in a new tab)</span>
          </a>
          <button type="button" class="btn-ghost" onclick={logout}>
            <LogOut class="w-4 h-4" aria-hidden="true" />
            <span>Log out</span>
          </button>
        </nav>
      </div>
    </header>

    <main class="max-w-7xl mx-auto px-4 sm:px-6 py-6 grid gap-6 lg:grid-cols-[22rem_minmax(0,1fr)] items-start">
      <div class="flex flex-col gap-6 lg:sticky lg:top-6">
        <!-- Share + uploads switch -->
        <section class="panel" aria-labelledby="share-heading">
          <h2 id="share-heading" class="font-display text-lg font-bold">Guest link</h2>
          <p class="text-sm text-stone-300 mt-1">Guests open this link or scan the QR on the wall. They can also type the room code at the home page.</p>
          <div class="mt-3 flex items-center gap-2">
            <a href={guestHref} target="_blank" rel="noopener" class="min-w-0 flex-1 truncate rounded-lg border border-stone-700 bg-stone-950 px-3 py-2.5 text-sm text-[#faf6f0] underline-offset-4 hover:underline">{guestUrl}</a>
            <button type="button" class="btn-ghost shrink-0" onclick={copyGuestLink} aria-live="polite">
              {#if copied}
                <Check class="w-4 h-4" aria-hidden="true" /><span>Copied</span>
              {:else}
                <Copy class="w-4 h-4" aria-hidden="true" /><span>Copy</span>
              {/if}
            </button>
          </div>
          {#if config}
            <div class="mt-4 flex items-center justify-between gap-3 rounded-lg border border-stone-700 px-3 py-2.5">
              <div>
                <p id="uploads-label" class="text-sm font-semibold">Uploads {config.uploadsOpen ? 'open' : 'closed'}</p>
                <p class="text-xs text-stone-400">{config.uploadsOpen ? 'Guests can send photos.' : 'The wall stays up; no new photos.'}</p>
              </div>
              <button
                type="button"
                role="switch"
                aria-checked={config.uploadsOpen}
                aria-labelledby="uploads-label"
                class="switch"
                disabled={togglingUploads}
                onclick={toggleUploads}
              >
                <span class="switch-knob" aria-hidden="true"></span>
              </button>
            </div>
          {/if}
        </section>

        <!-- Event settings -->
        <section class="panel" aria-labelledby="settings-heading">
          <h2 id="settings-heading" class="font-display text-lg font-bold">Event settings</h2>
          <p class="text-sm text-stone-300 mt-1">Changes reach the wall and every phone as soon as you save.</p>

          {#if !config}
            <p class="mt-4 text-sm text-stone-300" role="status">Loading settings</p>
          {:else}
            <form class="mt-4 flex flex-col gap-4" onsubmit={saveSettings} novalidate>
              <div>
                <div class="flex justify-between items-baseline">
                  <label for="f-title" class="label">Title</label>
                  <span class="text-xs text-stone-400">{fTitle.length}/80</span>
                </div>
                <input id="f-title" class="field" maxlength="80" bind:value={fTitle} aria-invalid={fieldErrors.title ? 'true' : undefined} aria-describedby={fieldErrors.title ? 'e-title' : undefined} />
                {#if fieldErrors.title}<p id="e-title" class="field-error">{fieldErrors.title}</p>{/if}
              </div>

              <div>
                <div class="flex justify-between items-baseline">
                  <label for="f-subtitle" class="label">Subtitle</label>
                  <span class="text-xs text-stone-400">{fSubtitle.length}/160</span>
                </div>
                <textarea id="f-subtitle" class="field min-h-20 resize-y" maxlength="160" bind:value={fSubtitle} aria-invalid={fieldErrors.subtitle ? 'true' : undefined} aria-describedby={fieldErrors.subtitle ? 'e-subtitle' : undefined}></textarea>
                {#if fieldErrors.subtitle}<p id="e-subtitle" class="field-error">{fieldErrors.subtitle}</p>{/if}
              </div>

              <div>
                <label for="f-max" class="label">Photos per person</label>
                <input id="f-max" type="number" min="1" max="50" step="1" class="field w-28" bind:value={fMax} aria-invalid={fieldErrors.maxPhotosPerPerson ? 'true' : undefined} aria-describedby="h-max{fieldErrors.maxPhotosPerPerson ? ' e-max' : ''}" />
                <p id="h-max" class="hint">Lowering it does not remove photos already sent.</p>
                {#if fieldErrors.maxPhotosPerPerson}<p id="e-max" class="field-error">{fieldErrors.maxPhotosPerPerson}</p>{/if}
              </div>

              <div>
                <label for="f-code" class="label">Event code</label>
                <input id="f-code" class="field" maxlength="40" autocomplete="off" spellcheck="false" bind:value={fCode} aria-invalid={fieldErrors.eventCode ? 'true' : undefined} aria-describedby="h-code{fieldErrors.eventCode ? ' e-code' : ''}" />
                <p id="h-code" class="hint">Leave empty to let anyone with the link upload. With a code, only people who scan the wall's QR can.</p>
                {#if fieldErrors.eventCode}<p id="e-code" class="field-error">{fieldErrors.eventCode}</p>{/if}
              </div>

              {#if saveMessage}
                <p class="text-sm {saveMessage.tone === 'ok' ? 'text-emerald-300' : 'text-amber-200'}" role="status">{saveMessage.text}</p>
              {/if}

              <div class="flex flex-wrap gap-2">
                <button type="submit" class="btn-primary" disabled={!dirty || saving}>
                  {#if saving}<LoaderCircle class="w-4 h-4 animate-spin" aria-hidden="true" />{/if}
                  <span>{saving ? 'Saving' : 'Save settings'}</span>
                </button>
                {#if dirty && !saving}
                  <button type="button" class="btn-ghost" onclick={() => config && fillForm(config)}>Undo changes</button>
                {/if}
              </div>

              {#if config.eventCode}
                <p class="hint">
                  The wall only puts the code in its QR when opened with it:
                  <a class="underline text-amber-300 break-all" href={wallHref} target="_blank" rel="noopener">{wallHref}</a>
                </p>
              {/if}
            </form>
          {/if}
        </section>

        <!-- Album -->
        <section class="panel" aria-labelledby="album-heading">
          <h2 id="album-heading" class="font-display text-lg font-bold">Photo album</h2>
          <p class="text-sm text-stone-300 mt-1">
            One ZIP with every photo on this wall ({visibleCount}), a <code class="text-stone-200">memories.json</code> list, and an
            <code class="text-stone-200">index.html</code> album that opens offline. Hidden photos are left out.
          </p>
          <a href={adminExportUrl(room)} download class="btn-primary mt-4 w-full" aria-disabled={visibleCount === 0 ? 'true' : undefined}>
            <Download class="w-4 h-4" aria-hidden="true" />
            <span>Download album (.zip)</span>
          </a>
        </section>

        <!-- Delete event -->
        <section class="panel" aria-labelledby="danger-heading">
          <h2 id="danger-heading" class="font-display text-lg font-bold">Delete event</h2>
          <p class="text-sm text-stone-300 mt-1">Removes the event and every photo in it. Download the album first if you want to keep them.</p>
          <button type="button" class="btn-action btn-danger mt-4 w-full" onclick={askDeleteEvent} disabled={!config}>
            <Trash2 class="w-4 h-4" aria-hidden="true" />
            <span>Delete this event</span>
          </button>
        </section>
      </div>

      <!-- Moderation -->
      <section aria-labelledby="photos-heading" class="min-w-0">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 id="photos-heading" class="font-display text-lg font-bold">Photos</h2>
            <p class="text-sm text-stone-300">Newest first. Hide takes a photo off the wall but keeps it; delete removes it for good.</p>
          </div>
          <div class="flex rounded-lg border border-white/15 p-1 gap-1" role="group" aria-label="Filter photos">
            {#each [['all', `All (${photos.length})`], ['visible', `On the wall (${visibleCount})`], ['hidden', `Hidden (${hiddenCount})`]] as [value, label] (value)}
              <button type="button" class="seg" aria-pressed={filter === value} onclick={() => (filter = value as Filter)}>{label}</button>
            {/each}
          </div>
        </div>

        {#if photosState === 'loading'}
          <p class="mt-10 text-center text-stone-300" role="status">Loading photos</p>
        {:else if photosState === 'error' && photos.length === 0}
          <div class="mt-10 text-center" role="alert">
            <p class="text-stone-200">{photosError}</p>
            <button type="button" class="btn-ghost mt-3" onclick={loadPhotos}>Try again</button>
          </div>
        {:else if filtered.length === 0}
          <p class="mt-10 text-center text-stone-300">
            {#if photos.length === 0}
              No photos yet. They show up here the moment someone uploads.
            {:else if filter === 'hidden'}
              Nothing is hidden. Photos you hide land here so you can bring them back.
            {:else}
              Every photo is hidden right now.
            {/if}
          </p>
        {:else}
          <ul class="mt-4 grid gap-4 grid-cols-[repeat(auto-fill,minmax(15rem,1fr))]">
            {#each filtered as photo (photo.id)}
              {@const used = photo.deviceId ? (deviceUsage.get(photo.deviceId) ?? 0) : 0}
              {@const wasReset = photo.deviceId?.startsWith('reset:')}
              <li class="rounded-xl border border-white/10 bg-stone-900/70 overflow-hidden flex flex-col">
                <div class="relative aspect-square bg-stone-800">
                  <img
                    src={photo.imageUrl}
                    alt={photo.caption ? `${photo.caption}, shared by ${photo.nickname}` : `Photo shared by ${photo.nickname}`}
                    width="400"
                    height="400"
                    loading="lazy"
                    class="w-full h-full object-cover {photo.hidden ? 'opacity-40 grayscale' : ''}"
                  />
                  {#if photo.hidden}
                    <span class="absolute top-2 left-2 rounded-md bg-stone-950/90 px-2 py-1 text-xs font-semibold text-amber-200">Hidden from the wall</span>
                  {/if}
                </div>
                <div class="p-3 flex flex-col gap-1 flex-1">
                  {#if photo.caption}
                    <p class="font-handwriting text-xl leading-tight text-stone-100 break-words">{photo.caption}</p>
                  {/if}
                  <p class="text-sm text-stone-300 flex justify-between gap-2">
                    <span class="font-semibold text-stone-200 truncate">@{photo.nickname}</span>
                    <time datetime={photo.createdAt} class="shrink-0">{relativeTime(photo.createdAt, now)}</time>
                  </p>
                  <p class="text-xs text-stone-400">
                    {#if wasReset}
                      Phone limit was reset after this upload
                    {:else}
                      This phone: {used} of {config?.maxPhotosPerPerson ?? 5} uploads used
                    {/if}
                  </p>
                </div>
                <div class="p-3 pt-0 grid grid-cols-2 gap-2">
                  <button type="button" class="btn-action col-span-2" disabled={photo.hidden || busy[photo.id]} onclick={() => sendToSpotlight(photo)} title={photo.hidden ? 'Unhide it first' : undefined}>
                    <Tv class="w-4 h-4" aria-hidden="true" />
                    <span>Spotlight on wall</span>
                  </button>
                  <button type="button" class="btn-action" disabled={busy[photo.id]} onclick={() => toggleHidden(photo)}>
                    {#if photo.hidden}
                      <Eye class="w-4 h-4" aria-hidden="true" /><span>Unhide</span>
                    {:else}
                      <EyeOff class="w-4 h-4" aria-hidden="true" /><span>Hide</span>
                    {/if}
                  </button>
                  <button type="button" class="btn-action btn-danger" disabled={busy[photo.id]} onclick={() => askDelete(photo)}>
                    <Trash2 class="w-4 h-4" aria-hidden="true" /><span>Delete</span>
                  </button>
                  {#if photo.deviceId && !wasReset && used > 0}
                    <button type="button" class="btn-link col-span-2" onclick={() => askReset(photo)}>
                      <RotateCcw class="w-3.5 h-3.5" aria-hidden="true" />
                      <span>Reset this phone's limit</span>
                    </button>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    </main>

    <dialog
      bind:this={dialogEl}
      class="confirm"
      aria-labelledby="confirm-title"
      onclose={() => {
        confirmState = null;
      }}
    >
      {#if confirmState}
        <div class="flex gap-4 items-start">
          {#if confirmState.photo}
            <img src={confirmState.photo.imageUrl} alt="" width="72" height="72" class="w-18 h-18 rounded-md object-cover shrink-0" />
          {/if}
          <div>
            <h2 id="confirm-title" class="font-display text-lg font-bold">{confirmState.title}</h2>
            <p class="mt-1.5 text-sm text-stone-300">{confirmState.body}</p>
          </div>
        </div>
        <div class="mt-6 flex flex-wrap justify-end gap-2">
          <button type="button" class="btn-ghost" onclick={() => dialogEl?.close()} disabled={confirmBusy}>Cancel</button>
          <button type="button" class="btn-primary {confirmState.photo || confirmState.danger ? 'btn-primary-danger' : ''}" onclick={runConfirm} disabled={confirmBusy}>
            {#if confirmBusy}<LoaderCircle class="w-4 h-4 animate-spin" aria-hidden="true" />{/if}
            <span>{confirmState.confirmLabel}</span>
          </button>
        </div>
      {/if}
    </dialog>

    <div class="fixed bottom-4 inset-x-4 flex justify-center pointer-events-none z-50" aria-live="polite">
      {#if toast}
        <p class="pointer-events-auto max-w-md rounded-lg px-4 py-3 text-sm font-medium shadow-lg {toast.tone === 'ok' ? 'bg-stone-100 text-stone-900' : 'bg-amber-200 text-stone-950'}">
          {toast.text}
        </p>
      {/if}
    </div>
  </div>
{/if}

<style>
  .panel {
    border-radius: 0.75rem;
    border: 1px solid rgba(255, 255, 255, 0.1);
    background: rgba(28, 25, 23, 0.7);
    padding: 1.25rem;
  }

  .label {
    display: block;
    font-size: 0.875rem;
    font-weight: 600;
    color: #e7e5e4;
    margin-bottom: 0.375rem;
  }

  .hint {
    margin-top: 0.375rem;
    font-size: 0.8rem;
    line-height: 1.4;
    color: #a8a29e;
  }

  .field-error {
    margin-top: 0.375rem;
    font-size: 0.8rem;
    color: #fde68a;
  }

  .field {
    display: block;
    width: 100%;
    min-height: 44px;
    border-radius: 0.5rem;
    border: 1px solid #57534e;
    background: #0c0a09;
    color: #faf6f0;
    padding: 0.55rem 0.75rem;
    font-size: 1rem;
  }

  .field:focus-visible {
    outline: 3px solid #fbbf24;
    outline-offset: 1px;
    border-color: #fbbf24;
  }

  .field[aria-invalid='true'] {
    border-color: #f59e0b;
  }

  .field:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .btn-primary,
  .btn-ghost,
  .btn-action,
  .btn-link,
  .seg {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.45rem;
    font-weight: 600;
    font-size: 0.875rem;
    border-radius: 0.5rem;
    transition:
      background-color 120ms ease,
      border-color 120ms ease,
      transform 80ms ease;
  }

  .btn-primary {
    min-height: 44px;
    padding: 0 1.1rem;
    background: #f59e0b;
    color: #1c1917;
    border: 1px solid #f59e0b;
    text-decoration: none;
  }

  .btn-primary:hover:not(:disabled) {
    background: #fbbf24;
  }

  .btn-primary-danger {
    background: #fca5a5;
    border-color: #fca5a5;
  }

  .btn-primary-danger:hover:not(:disabled) {
    background: #fecaca;
  }

  .btn-ghost {
    min-height: 44px;
    padding: 0 1rem;
    border: 1px solid rgba(250, 246, 240, 0.22);
    background: rgba(28, 25, 23, 0.6);
    color: #faf6f0;
    text-decoration: none;
  }

  .btn-ghost:hover:not(:disabled) {
    background: #292524;
    border-color: rgba(250, 246, 240, 0.45);
  }

  .btn-action {
    min-height: 44px;
    padding: 0 0.75rem;
    border: 1px solid rgba(250, 246, 240, 0.18);
    background: #1c1917;
    color: #f5f5f4;
  }

  .btn-action:hover:not(:disabled) {
    background: #292524;
    border-color: rgba(250, 246, 240, 0.4);
  }

  .btn-danger {
    color: #fecaca;
  }

  .btn-danger:hover:not(:disabled) {
    border-color: #fca5a5;
  }

  .btn-link {
    min-height: 44px;
    color: #d6d3d1;
    text-decoration: underline;
    text-underline-offset: 3px;
  }

  .btn-link:hover {
    color: #fde68a;
  }

  .seg {
    min-height: 40px;
    padding: 0 0.75rem;
    color: #d6d3d1;
  }

  .seg:hover {
    background: #292524;
  }

  .seg[aria-pressed='true'] {
    background: #faf6f0;
    color: #1c1917;
  }

  .btn-primary:active:not(:disabled),
  .btn-ghost:active:not(:disabled),
  .btn-action:active:not(:disabled) {
    transform: translateY(1px);
  }

  .btn-primary:disabled,
  .btn-ghost:disabled,
  .btn-action:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .btn-primary[aria-disabled='true'] {
    opacity: 0.45;
    pointer-events: none;
  }

  .btn-primary:focus-visible,
  .btn-ghost:focus-visible,
  .btn-action:focus-visible,
  .btn-link:focus-visible,
  .seg:focus-visible {
    outline: 3px solid #fbbf24;
    outline-offset: 2px;
  }

  .confirm {
    width: min(30rem, calc(100vw - 2rem));
    margin: auto;
    border-radius: 0.75rem;
    border: 1px solid rgba(255, 255, 255, 0.15);
    background: #1c1917;
    color: #faf6f0;
    padding: 1.5rem;
  }

  .confirm::backdrop {
    background: rgba(12, 10, 9, 0.75);
  }

  /* Uploads open/closed switch: a real two-state control, amber when open. */
  .switch {
    position: relative;
    flex-shrink: 0;
    width: 52px;
    height: 30px;
    border-radius: 999px;
    background: #44403c;
    border: 1px solid #57534e;
    transition: background-color 120ms ease;
  }

  .switch[aria-checked='true'] {
    background: #f59e0b;
    border-color: #f59e0b;
  }

  .switch-knob {
    position: absolute;
    top: 3px;
    left: 3px;
    width: 22px;
    height: 22px;
    border-radius: 999px;
    background: #faf6f0;
    transition: transform 120ms ease;
  }

  .switch[aria-checked='true'] .switch-knob {
    transform: translateX(22px);
  }

  .switch:focus-visible {
    outline: 3px solid #fbbf24;
    outline-offset: 2px;
  }

  .switch:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  /* The real 44px tap target wraps the 30px-tall visual switch. */
  .switch::after {
    content: '';
    position: absolute;
    inset: -8px -4px;
  }

  @media (prefers-reduced-motion: reduce) {
    .btn-primary,
    .btn-ghost,
    .btn-action,
    .seg {
      transition: none;
    }
  }
</style>
