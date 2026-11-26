<script lang="ts">
  import { onMount } from 'svelte';
  import { flip } from 'svelte/animate';
  import { crossfade, fade, scale } from 'svelte/transition';
  import { backOut, cubicOut } from 'svelte/easing';
  import { prefersReducedMotion } from 'svelte/motion';
  import { Pause, Play, Maximize, Minimize, LoaderCircle, Camera } from 'lucide-svelte';

  import { fetchConfig, fetchPhotos, isRoomNotFound, type Photo, type PublicConfigResponse } from '../lib/api';
  import { connectSSE } from '../lib/sse';
  import { buildUploadTarget, computeWallLayout, sortNewestFirst, tiltFor } from '../lib/wall';
  import Polaroid from '../components/Polaroid.svelte';
  import QrCard from '../components/QrCard.svelte';
  import EmojiStream from '../components/EmojiStream.svelte';

  // ---- Timing (ms) ----
  const HOLD_NEW = 5000;
  const HOLD_NEW_BACKLOG = 3000; // used when 3+ uploads are waiting, so a burst does not take minutes
  const HOLD_MEMORY = 6000;
  const HOLD_HOST = 8000;
  const GAP_BETWEEN_SPOTLIGHTS = 1300; // longer than the crossfade so the stage is empty before the next card
  const IDLE_BEFORE_MEMORY = 20000;
  const MAX_QUEUE = 12;
  const CONTROLS_HIDE_AFTER = 3000;
  const RETRY_AFTER = 5000;
  const BOARD_INSET = 28; // room for tilted corners and tape so nothing clips at the board edge

  type SpotKind = 'new' | 'memory' | 'host';
  interface SpotItem {
    photo: Photo;
    kind: SpotKind;
  }

  let { room }: { room: string } = $props();
  let notFound = $state(false);

  let config = $state<PublicConfigResponse>({
    room: '',
    title: '',
    subtitle: '',
    maxPhotosPerPerson: 5,
    requiresEventCode: false,
    uploadsOpen: true,
  });

  let photos = $state<Photo[]>([]);
  let loadState = $state<'loading' | 'ready' | 'error'>('loading');
  let loadError = $state('');
  let connected = $state(false);
  let now = $state(Date.now());

  // ---- Layout ----
  let viewportW = $state(typeof window !== 'undefined' ? window.innerWidth : 1280);
  let boardW = $state(0);
  let boardH = $state(0);
  let isCompact = $derived(viewportW < 768);
  let layout = $derived(
    computeWallLayout(boardW - BOARD_INSET * 2, boardH - BOARD_INSET * 2, photos.length),
  );
  let visible = $derived(isCompact ? photos : photos.slice(0, layout.capacity));
  let offWall = $derived(Math.max(0, photos.length - visible.length));

  // ---- Spotlight ----
  let queue = $state<SpotItem[]>([]);
  let current = $state<SpotItem | null>(null);
  // What the stage renders. Unlike `current`, it is never cleared, so the card can still play its exit flight after `current` becomes null.
  let stageItem = $state<SpotItem | null>(null);
  let paused = $state(false);
  let announcement = $state('');
  let pendingIds = $derived(new Set(queue.filter((q) => q.kind === 'new').map((q) => q.photo.id)));
  let holdTimer: ReturnType<typeof setTimeout> | null = null;
  let gapTimer: ReturnType<typeof setTimeout> | null = null;
  let lastActivityAt = Date.now();
  let recentIds: string[] = [];

  // ---- Chrome ----
  let controlsVisible = $state(true);
  let controlsFocused = false;
  let hideTimer: ReturnType<typeof setTimeout> | null = null;
  let isFullscreen = $state(false);
  const fullscreenSupported =
    typeof document !== 'undefined' && !!document.fullscreenEnabled && !!document.documentElement.requestFullscreen;

  let emojiStream = $state<ReturnType<typeof EmojiStream> | null>(null);
  // The page is keyed by room in App.svelte, so room never changes for this instance.
  // svelte-ignore state_referenced_locally
  const uploadTarget = buildUploadTarget(window.location, room);

  let reduced = $derived(prefersReducedMotion.current);

  const [send, receive] = crossfade({
    duration: (d) => Math.min(1100, Math.max(600, Math.sqrt(d) * 32)),
    easing: cubicOut,
    fallback(node, _params, intro) {
      if (prefersReducedMotion.current) return fade(node, { duration: 250 });
      if (intro) {
        // A brand-new print slides up from below the screen edge, like it just came out of the camera.
        return {
          duration: 950,
          easing: backOut,
          css: (t, u) =>
            `transform: translateY(${u * 55}vh) rotate(${u * -10}deg) scale(${0.7 + 0.3 * t}); opacity: ${Math.min(1, t * 2.5)}`,
        };
      }
      return scale(node, { duration: 500, start: 0.6, opacity: 0, easing: cubicOut });
    },
  });

  function crossfadeParams(key: string) {
    return reduced ? { key, duration: 0 } : { key };
  }

  // ---- Spotlight queue ----
  function enqueue(item: SpotItem) {
    if (paused && item.kind !== 'host') return;
    const id = item.photo.id;
    if (current?.photo.id === id || queue.some((q) => q.photo.id === id)) return;
    if (item.kind === 'host') {
      queue = [item, ...queue];
    } else {
      if (queue.length >= MAX_QUEUE) return; // during a big burst the extra photos simply land on the wall
      queue = [...queue, item];
    }
    pump();
  }

  function pump() {
    if (current || gapTimer || queue.length === 0) return;
    const [next, ...rest] = queue;
    queue = rest;
    current = next;
    stageItem = next;
    rememberRecent(next.photo.id);

    if (next.kind === 'new') {
      announcement = `New photo from ${next.photo.nickname}${next.photo.caption ? `: ${next.photo.caption}` : ''}`;
    }

    const hold =
      next.kind === 'host'
        ? HOLD_HOST
        : next.kind === 'memory'
          ? HOLD_MEMORY
          : rest.length >= 3
            ? HOLD_NEW_BACKLOG
            : HOLD_NEW;
    holdTimer = setTimeout(finishSpotlight, hold);
  }

  function finishSpotlight() {
    if (holdTimer) {
      clearTimeout(holdTimer);
      holdTimer = null;
    }
    if (!current) return;
    current = null;
    lastActivityAt = Date.now();
    gapTimer = setTimeout(() => {
      gapTimer = null;
      pump();
    }, GAP_BETWEEN_SPOTLIGHTS);
  }

  function rememberRecent(id: string) {
    recentIds = [id, ...recentIds.filter((r) => r !== id)].slice(0, 8);
  }

  function maybeSpotlightMemory() {
    if (paused || current || gapTimer || queue.length > 0) return;
    if (photos.length < 2 || document.hidden) return;
    if (Date.now() - lastActivityAt < IDLE_BEFORE_MEMORY) return;

    const avoid = new Set(recentIds.slice(0, Math.min(6, photos.length - 1)));
    const candidates = photos.filter((p) => !avoid.has(p.id));
    const pick = candidates[Math.floor(Math.random() * candidates.length)];
    if (pick) enqueue({ photo: pick, kind: 'memory' });
  }

  function togglePause() {
    paused = !paused;
    if (paused) {
      queue = queue.filter((q) => q.kind === 'host');
      if (current && current.kind !== 'host') finishSpotlight();
    } else {
      lastActivityAt = Date.now();
    }
  }

  // ---- Data ----
  async function loadPhotos() {
    try {
      const res = await fetchPhotos(room);
      photos = sortNewestFirst(res.photos || []);
      const ids = new Set(photos.map((p) => p.id));
      queue = queue.filter((q) => ids.has(q.photo.id));
      loadState = 'ready';
      loadError = '';
    } catch (err) {
      if (isRoomNotFound(err)) {
        notFound = true;
        return;
      }
      if (loadState !== 'ready') {
        loadState = 'error';
        loadError = err instanceof Error ? err.message : 'Unknown error';
        scheduleRetry();
      }
    }
  }

  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  function scheduleRetry() {
    if (retryTimer) clearTimeout(retryTimer);
    retryTimer = setTimeout(() => {
      retryTimer = null;
      loadPhotos();
    }, RETRY_AFTER);
  }

  function retryNow() {
    if (retryTimer) clearTimeout(retryTimer);
    retryTimer = null;
    loadState = 'loading';
    loadPhotos();
  }

  function handlePhotoAdded(photo: Photo) {
    lastActivityAt = Date.now();
    if (photos.some((p) => p.id === photo.id)) return;
    photos = sortNewestFirst([photo, ...photos]);
    if (loadState !== 'ready') loadState = 'ready';
    // A photo the host unhid is old news: put it back on the wall without the "Just pinned" reveal.
    const isFresh = Date.now() - Date.parse(photo.createdAt) < 2 * 60_000;
    if (!isFresh) return;
    enqueue({ photo, kind: 'new' });
    if (paused) announcement = `New photo from ${photo.nickname}`;
  }

  function handlePhotoDeleted(id: string) {
    photos = photos.filter((p) => p.id !== id);
    queue = queue.filter((q) => q.photo.id !== id);
    recentIds = recentIds.filter((r) => r !== id);
    if (current?.photo.id === id) finishSpotlight();
  }

  // ---- Chrome: auto-hiding controls, fullscreen, shortcuts ----
  function poke() {
    controlsVisible = true;
    if (hideTimer) clearTimeout(hideTimer);
    hideTimer = setTimeout(() => {
      if (!controlsFocused) controlsVisible = false;
    }, CONTROLS_HIDE_AFTER);
  }

  async function toggleFullscreen() {
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else await document.documentElement.requestFullscreen();
    } catch {
      // Browser refused (e.g. not triggered by a user gesture); nothing to recover.
    }
  }

  function onKeydown(e: KeyboardEvent) {
    poke();
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const target = e.target as HTMLElement | null;
    if (target && target.closest('input, textarea, select, [contenteditable]')) return;
    const key = e.key.toLowerCase();
    if (key === 'f' && fullscreenSupported) {
      e.preventDefault();
      toggleFullscreen();
    } else if (key === 'p') {
      e.preventDefault();
      togglePause();
    }
  }

  onMount(() => {
    fetchConfig(room)
      .then((c) => (config = c))
      .catch((err) => {
        if (isRoomNotFound(err)) notFound = true;
      });
    loadPhotos();

    let hadDisconnect = false;
    const sse = connectSSE(room, {
      onConnectionChange: (isConn) => {
        if (isConn && hadDisconnect) {
          // Catch up on anything that happened while the stream was down.
          hadDisconnect = false;
          loadPhotos();
          fetchConfig(room)
            .then((c) => (config = c))
            .catch(() => {});
        }
        if (!isConn && connected) hadDisconnect = true;
        connected = isConn;
      },
      onRoomDeleted: () => {
        notFound = true;
        sse.close();
      },
      onPhotoAdded: handlePhotoAdded,
      onPhotoDeleted: (payload) => {
        if (payload && typeof payload.id === 'string') handlePhotoDeleted(payload.id);
      },
      onPhotoSpotlight: (photo) => {
        const known = photos.find((p) => p.id === photo.id) ?? photo;
        enqueue({ photo: known, kind: 'host' });
      },
      onReaction: (reaction) => emojiStream?.push(reaction),
      onConfigUpdated: (updated) => {
        config = { ...config, ...updated };
      },
    });

    const clock = setInterval(() => (now = Date.now()), 30_000);
    const idle = setInterval(maybeSpotlightMemory, 3000);
    const onFullscreenChange = () => (isFullscreen = !!document.fullscreenElement);
    document.addEventListener('fullscreenchange', onFullscreenChange);
    poke();

    return () => {
      sse.close();
      clearInterval(clock);
      clearInterval(idle);
      document.removeEventListener('fullscreenchange', onFullscreenChange);
      for (const t of [holdTimer, gapTimer, hideTimer, retryTimer]) if (t) clearTimeout(t);
    };
  });

  let stageLabel = $derived(
    stageItem?.kind === 'new' ? 'Just pinned' : stageItem?.kind === 'host' ? "Host's pick" : 'Looking back',
  );
  let waitingNew = $derived(queue.filter((q) => q.kind === 'new').length);
</script>

<svelte:window bind:innerWidth={viewportW} onkeydown={onKeydown} onmousemove={poke} ontouchstart={poke} />

{#if notFound}
  <main class="wall-gone darkroom-bg">
    <div role="alert">
      <h1 class="font-display">This event is not here</h1>
      <p>There is no event with room code <code>{room}</code>. It may have been deleted. Check the link in the host panel.</p>
    </div>
  </main>
{:else}
<div class="wall-root darkroom-bg" class:hide-cursor={!controlsVisible && !isCompact}>
  <header class="wall-header">
    <div class="titles">
      <h1 class="font-display title">{config.title}</h1>
      <p class="subtitle">{config.subtitle}</p>
    </div>

    <div class="header-side">
    <p class="status" class:status-down={!connected} class:controls-hidden={connected && !controlsVisible && !isCompact}>
      <span class="dot" aria-hidden="true"></span>
      <span>{connected ? 'Live' : 'Reconnecting to the live feed'}</span>
    </p>
    <div
      class="controls"
      class:controls-hidden={!controlsVisible && !isCompact}
      onfocusin={() => {
        controlsFocused = true;
        poke();
      }}
      onfocusout={() => {
        controlsFocused = false;
        poke();
      }}
    >
      {#if isCompact}
        <a href="/" class="btn btn-primary">
          <Camera class="w-4 h-4" aria-hidden="true" />
          <span>Pin your photos</span>
        </a>
      {/if}
      <button type="button" class="btn" onclick={togglePause} aria-pressed={paused} title="Shortcut: P">
        {#if paused}
          <Play class="w-4 h-4" aria-hidden="true" />
          <span>Resume highlights</span>
        {:else}
          <Pause class="w-4 h-4" aria-hidden="true" />
          <span>Pause highlights</span>
        {/if}
      </button>
      {#if fullscreenSupported && !isCompact}
        <button type="button" class="btn" onclick={toggleFullscreen} title="Shortcut: F">
          {#if isFullscreen}
            <Minimize class="w-4 h-4" aria-hidden="true" />
            <span>Exit fullscreen</span>
          {:else}
            <Maximize class="w-4 h-4" aria-hidden="true" />
            <span>Fullscreen</span>
          {/if}
        </button>
      {/if}
    </div>

    </div>
  </header>

  <div class="wall-body">
    <main class="board" bind:clientWidth={boardW} bind:clientHeight={boardH}>
      {#if loadState === 'loading' && photos.length === 0}
        <div class="board-message" role="status">
          <LoaderCircle class="w-8 h-8 spin text-amber-400" aria-hidden="true" />
          <p class="msg-title font-handwriting">Loading the wall</p>
        </div>
      {:else if loadState === 'error' && photos.length === 0}
        <div class="board-message" role="alert">
          <p class="msg-title font-handwriting">The photos could not be loaded</p>
          <p class="msg-body">{loadError}. Trying again every few seconds.</p>
          <button type="button" class="btn" onclick={retryNow}>Retry now</button>
        </div>
      {:else if photos.length === 0}
        <div class="board-message">
          <p class="msg-title font-handwriting">Nothing pinned yet</p>
          <p class="msg-body">
            {#if isCompact}
              Open the uploader and add the first photo of the night.
            {:else}
              Scan the code on the right and the first photo will land here.
            {/if}
          </p>
        </div>
      {:else}
        <ul
          class="grid-wall"
          class:compact={isCompact}
          style={isCompact
            ? ''
            : `grid-template-columns: repeat(${layout.cols}, ${layout.cardWidth}px); gap: ${layout.gap}px; padding: ${BOARD_INSET}px;`}
        >
          {#each visible as photo (photo.id)}
            <li
              class="slot"
              animate:flip={{ duration: reduced ? 0 : 700, easing: cubicOut }}
              in:fade={{ duration: reduced ? 0 : 400 }}
              out:scale={{ duration: reduced ? 0 : 400, start: 0.7 }}
            >
              {#if pendingIds.has(photo.id)}
                <div class="undeveloped-frame">
                  <div class="undeveloped polaroid-paper" style="--tilt: {tiltFor(photo.id)}deg">
                    <div class="undeveloped-photo"></div>
                    <p class="font-handwriting">developing</p>
                  </div>
                </div>
              {:else if current?.photo.id === photo.id}
                <div class="ghost" style="--tilt: {tiltFor(photo.id)}deg" aria-hidden="true"></div>
              {:else}
                <div in:receive={crossfadeParams(photo.id)} out:send={crossfadeParams(photo.id)}>
                  <Polaroid {photo} {now} />
                </div>
              {/if}
            </li>
          {/each}
        </ul>
        {#if offWall > 0 && !isCompact}
          <p class="off-wall">+{offWall} older {offWall === 1 ? 'photo' : 'photos'} in the highlight rotation</p>
        {/if}
      {/if}
    </main>

    <aside class="rail" aria-label="How to add photos">
      <QrCard
        target={uploadTarget}
        {room}
        count={photos.length}
        countKnown={loadState === 'ready'}
        maxPerPerson={config.maxPhotosPerPerson}
        requiresEventCode={config.requiresEventCode}
      />
    </aside>
  </div>

  {#if current && stageItem}
    <div class="backdrop" transition:fade={{ duration: reduced ? 0 : 400 }} aria-hidden="true"></div>
    {#if stageItem.kind === 'new' && !reduced}
      <div class="flash" aria-hidden="true"></div>
    {/if}
    <div class="stage" aria-hidden="true">
      <p class="stage-label font-display" transition:fade={{ duration: reduced ? 0 : 300 }}>{stageLabel}</p>
      <div class="stage-card" in:receive={crossfadeParams(stageItem.photo.id)} out:send={crossfadeParams(stageItem.photo.id)}>
        <Polaroid photo={stageItem.photo} {now} developing={stageItem.kind === 'new'} tilt={-1.5} eager />
      </div>
      <p class="stage-queue" transition:fade={{ duration: reduced ? 0 : 300 }}>
        {#if waitingNew > 0}
          {waitingNew} more {waitingNew === 1 ? 'photo' : 'photos'} developing
        {/if}
      </p>
    </div>
  {/if}

  <p class="sr-only" aria-live="polite">{announcement}</p>

  <EmojiStream bind:this={emojiStream} />
</div>
{/if}

<style>
  .wall-gone {
    min-height: 100dvh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 2rem;
    text-align: center;
    color: #faf6f0;
  }

  .wall-gone h1 {
    margin: 0;
    font-size: clamp(1.8rem, 3.5vw, 3.5rem);
  }

  .wall-gone p {
    margin: 0.75rem auto 0;
    max-width: 40ch;
    font-size: clamp(1rem, 1.4vw, 1.5rem);
    color: #d6d3d1;
  }

  .wall-root {
    min-height: 100dvh;
    display: flex;
    flex-direction: column;
    color: #faf6f0;
  }

  .hide-cursor,
  .hide-cursor :global(*) {
    cursor: none !important;
  }

  .wall-header {
    position: relative;
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    justify-content: space-between;
    gap: 0.75rem 1.5rem;
    padding: 1.25rem 1.25rem 0.5rem;
  }

  .titles {
    min-width: 0;
    flex: 1 1 20rem;
  }

  .title {
    margin: 0;
    font-size: clamp(1.6rem, 2.7vw, 3.4rem);
    font-weight: 700;
    line-height: 1.05;
    letter-spacing: -0.01em;
    overflow-wrap: anywhere;
  }

  .subtitle {
    margin: 0.4rem 0 0;
    max-width: 60ch;
    font-size: clamp(0.95rem, 1.2vw, 1.4rem);
    color: #d6d3d1;
  }

  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    transition: opacity 300ms ease;
  }

  .controls-hidden {
    opacity: 0;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    min-height: 44px;
    padding: 0 1rem;
    border-radius: 0.6rem;
    border: 1px solid rgba(250, 246, 240, 0.22);
    background: rgba(28, 25, 23, 0.85);
    color: #faf6f0;
    font-size: 0.9rem;
    font-weight: 600;
    cursor: pointer;
    transition:
      background-color 150ms ease,
      border-color 150ms ease,
      transform 100ms ease;
  }

  .btn:hover {
    background: #292524;
    border-color: rgba(250, 246, 240, 0.45);
  }

  .btn:active {
    transform: translateY(1px) scale(0.98);
  }

  .btn:focus-visible {
    outline: 3px solid #fbbf24;
    outline-offset: 2px;
  }

  .btn[aria-pressed='true'] {
    border-color: #fbbf24;
    color: #fde68a;
  }

  .btn-primary {
    background: #f59e0b;
    border-color: #f59e0b;
    color: #1c1917;
    text-decoration: none;
  }

  .btn-primary:hover {
    background: #fbbf24;
    border-color: #fbbf24;
  }

  .header-side {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 0.5rem 1rem;
  }

  .status {
    margin: 0;
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.75rem;
    font-weight: 600;
    color: #d6d3d1;
    transition: opacity 300ms ease;
  }

  .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 999px;
    background: #4ade80;
  }

  .status-down {
    color: #fde68a;
  }

  .status-down .dot {
    background: #f59e0b;
  }

  .wall-body {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 1.5rem;
    padding: 0.75rem 1.25rem 1.5rem;
  }

  .board {
    position: relative;
    flex: 1;
    min-width: 0;
    min-height: 0;
    border-radius: 1rem;
    background: rgba(250, 246, 240, 0.025);
    box-shadow: inset 0 0 0 1px rgba(250, 246, 240, 0.06);
  }

  .grid-wall {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    justify-content: center;
    align-content: center;
    height: 100%;
  }

  .grid-wall.compact {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1.5rem 1rem;
    padding: 1.5rem 1rem;
    height: auto;
  }

  .slot {
    position: relative;
    min-width: 0;
  }

  .undeveloped-frame {
    container-type: inline-size;
  }

  .undeveloped {
    padding: 5cqw 5cqw 0;
    aspect-ratio: 1 / 1.23;
    border-radius: 0.6cqw;
    transform: rotate(var(--tilt));
  }

  .undeveloped-photo {
    aspect-ratio: 1 / 1;
    border-radius: 0.4cqw;
    background: linear-gradient(160deg, #3b352f 0%, #1f1b17 100%);
  }

  .undeveloped p {
    margin: 3cqw 0 0;
    font-size: 7.6cqw;
    color: #57534e;
  }

  .ghost {
    aspect-ratio: 1 / 1.23;
    border: 2px dashed rgba(250, 246, 240, 0.22);
    border-radius: 0.5rem;
    transform: rotate(var(--tilt));
  }

  .off-wall {
    position: absolute;
    right: 1rem;
    bottom: 0.4rem;
    margin: 0;
    font-size: 0.8rem;
    color: #a8a29e;
  }

  .board-message {
    min-height: 16rem;
    height: 100%;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.6rem;
    padding: 2rem 1.5rem;
    text-align: center;
  }

  .msg-title {
    margin: 0;
    font-size: clamp(2.2rem, 4vw, 4.5rem);
    line-height: 1;
    color: #faf6f0;
  }

  .msg-body {
    margin: 0;
    max-width: 36ch;
    font-size: clamp(1rem, 1.3vw, 1.5rem);
    color: #d6d3d1;
  }

  .rail {
    flex-shrink: 0;
    width: min(100%, 22rem);
    margin: 0 auto;
    padding-top: 1rem;
  }

  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 40;
    background: rgba(12, 10, 9, 0.84);
  }

  .flash {
    position: fixed;
    inset: 0;
    z-index: 45;
    pointer-events: none;
    background: #fffaf0;
    animation: flash 700ms ease-out forwards;
  }

  .stage {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 2rem;
    pointer-events: none;
  }

  .stage-label {
    margin: 0;
    font-size: clamp(1.1rem, 1.8vw, 2.2rem);
    font-weight: 700;
    color: #fbbf24;
  }

  .stage-card {
    width: min(84vw, 40vw + 12rem, calc((100dvh - 16rem) / 1.23));
  }

  .stage-queue {
    margin: 0;
    min-height: 1.5em;
    font-size: clamp(0.95rem, 1.1vw, 1.3rem);
    color: #d6d3d1;
  }

  :global(.spin) {
    animation: spin 1s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  @keyframes flash {
    0% {
      opacity: 0.35;
    }
    100% {
      opacity: 0;
    }
  }

  @media (min-width: 768px) {
    .wall-root {
      height: 100dvh;
      overflow: hidden;
    }

    .wall-header {
      padding: 1.75rem 2.5vw 0.5rem;
    }

    .wall-body {
      flex-direction: row;
      gap: 2vw;
      padding: 0.75rem 2.5vw 2vw;
    }

    .rail {
      width: clamp(220px, 21vw, 400px);
      margin: 0;
      padding-top: 0;
      display: flex;
      flex-direction: column;
      justify-content: center;
      overflow-y: auto;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    :global(.spin) {
      animation-duration: 3s;
    }
  }
</style>
