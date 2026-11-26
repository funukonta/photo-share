<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, createAdminEvent, fetchAdminEvents, type AdminEvent } from '../lib/api';
  import { useHostSession } from '../components/HostGate.svelte';
  import { relativeTime } from '../lib/wall';
  import { Check, Copy, ExternalLink, LoaderCircle, LogOut, Plus, Settings, Tv } from 'lucide-svelte';

  const host = useHostSession();

  let events = $state<AdminEvent[]>([]);
  let listState = $state<'loading' | 'ready' | 'error'>('loading');
  let listError = $state('');
  let now = $state(Date.now());

  // Create form
  let title = $state('');
  let subtitle = $state('');
  let maxPerPerson = $state(5);
  let creating = $state(false);
  let fieldErrors = $state<Record<string, string>>({});
  let createError = $state('');
  let justCreated = $state<string>('');
  let copiedRoom = $state('');
  let titleInput = $state<HTMLInputElement | null>(null);

  const origin = typeof window !== 'undefined' ? window.location.origin : '';

  function errorText(err: unknown, fallback: string): string {
    if (err instanceof ApiError) return err.message;
    if (err instanceof TypeError) return 'The server could not be reached. Check the connection and try again.';
    return fallback;
  }

  async function load() {
    try {
      const res = await fetchAdminEvents();
      events = res.events || [];
      listState = 'ready';
      listError = '';
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) return host.expire();
      if (listState !== 'ready') listState = 'error';
      listError = errorText(err, 'Events could not be loaded.');
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (creating) return;
    fieldErrors = {};
    createError = '';
    if (!title.trim()) {
      fieldErrors = { title: 'Give the event a title.' };
      titleInput?.focus();
      return;
    }
    creating = true;
    try {
      const ev = await createAdminEvent({
        title: title.trim(),
        subtitle: subtitle.trim(),
        maxPhotosPerPerson: Number(maxPerPerson),
      });
      events = [ev, ...events];
      justCreated = ev.id;
      title = '';
      subtitle = '';
      maxPerPerson = 5;
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) return host.expire();
      if (err instanceof ApiError && err.data && typeof err.data.fields === 'object' && err.data.fields) {
        fieldErrors = err.data.fields as Record<string, string>;
      } else {
        createError = errorText(err, 'The event could not be created.');
      }
    } finally {
      creating = false;
    }
  }

  async function copyLink(room: string) {
    try {
      await navigator.clipboard.writeText(`${origin}/${room}`);
      copiedRoom = room;
      setTimeout(() => (copiedRoom = ''), 2000);
    } catch {
      createError = `Copy failed. The guest link is ${origin}/${room}`;
    }
  }

  onMount(() => {
    load();
    const clock = setInterval(() => (now = Date.now()), 30_000);
    return () => clearInterval(clock);
  });
</script>

<div class="min-h-dvh darkroom-bg text-[#faf6f0]">
  <header class="border-b border-white/10">
    <div class="max-w-6xl mx-auto px-4 sm:px-6 py-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="font-display text-xl sm:text-2xl font-bold">Events</h1>
        <p class="text-sm text-stone-300">Each event gets its own room code, wall, photos, and album.</p>
      </div>
      <button type="button" class="btn-ghost" onclick={() => host.logout()}>
        <LogOut class="w-4 h-4" aria-hidden="true" />
        <span>Log out</span>
      </button>
    </div>
  </header>

  <main class="max-w-6xl mx-auto px-4 sm:px-6 py-6 grid gap-6 lg:grid-cols-[22rem_minmax(0,1fr)] items-start">
    <!-- Create -->
    <section class="panel lg:sticky lg:top-6" aria-labelledby="create-heading">
      <h2 id="create-heading" class="font-display text-lg font-bold">New event</h2>
      <p class="text-sm text-stone-300 mt-1">You get a room code and guest link right away.</p>
      <form class="mt-4 flex flex-col gap-4" onsubmit={create} novalidate>
        <div>
          <div class="flex justify-between items-baseline">
            <label for="n-title" class="label">Title</label>
            <span class="text-xs text-stone-400">{title.length}/80</span>
          </div>
          <input
            id="n-title"
            bind:this={titleInput}
            class="field"
            maxlength="80"
            bind:value={title}
            placeholder="Batch 7 Graduation"
            aria-invalid={fieldErrors.title ? 'true' : undefined}
            aria-describedby={fieldErrors.title ? 'n-title-error' : undefined}
          />
          {#if fieldErrors.title}<p id="n-title-error" class="field-error" role="alert">{fieldErrors.title}</p>{/if}
        </div>
        <div>
          <div class="flex justify-between items-baseline">
            <label for="n-subtitle" class="label">Subtitle <span class="font-normal text-stone-400">(optional)</span></label>
            <span class="text-xs text-stone-400">{subtitle.length}/160</span>
          </div>
          <textarea
            id="n-subtitle"
            class="field min-h-20 resize-y"
            maxlength="160"
            bind:value={subtitle}
            placeholder="Scan the QR and send your favorite moments"
            aria-invalid={fieldErrors.subtitle ? 'true' : undefined}
          ></textarea>
          {#if fieldErrors.subtitle}<p class="field-error" role="alert">{fieldErrors.subtitle}</p>{/if}
        </div>
        <div>
          <label for="n-max" class="label">Photos per person</label>
          <input id="n-max" type="number" min="1" max="50" class="field w-28" bind:value={maxPerPerson} aria-invalid={fieldErrors.maxPhotosPerPerson ? 'true' : undefined} />
          {#if fieldErrors.maxPhotosPerPerson}<p class="field-error" role="alert">{fieldErrors.maxPhotosPerPerson}</p>{/if}
        </div>
        {#if createError}<p class="text-sm text-amber-200" role="alert">{createError}</p>{/if}
        <button type="submit" class="btn-primary" disabled={creating}>
          {#if creating}
            <LoaderCircle class="w-4 h-4 animate-spin" aria-hidden="true" /><span>Creating</span>
          {:else}
            <Plus class="w-4 h-4" aria-hidden="true" /><span>Create event</span>
          {/if}
        </button>
      </form>
    </section>

    <!-- List -->
    <section aria-labelledby="list-heading" class="min-w-0">
      <h2 id="list-heading" class="font-display text-lg font-bold">All events</h2>

      {#if listState === 'loading'}
        <p class="mt-8 text-center text-stone-300" role="status">Loading events</p>
      {:else if listState === 'error' && events.length === 0}
        <div class="mt-8 text-center" role="alert">
          <p class="text-stone-200">{listError}</p>
          <button type="button" class="btn-ghost mt-3" onclick={load}>Try again</button>
        </div>
      {:else if events.length === 0}
        <p class="mt-8 text-center text-stone-300">No events yet. Create the first one with the form.</p>
      {:else}
        <ul class="mt-4 flex flex-col gap-3">
          {#each events as ev (ev.id)}
            <li class="event-row {justCreated === ev.id ? 'event-row-new' : ''}">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <a href="/admin/{ev.id}" class="font-display text-lg font-bold hover:underline underline-offset-4 truncate">{ev.title}</a>
                  {#if !ev.uploadsOpen}
                    <span class="rounded bg-stone-800 px-1.5 py-0.5 text-xs font-semibold text-stone-200">Uploads closed</span>
                  {/if}
                  {#if justCreated === ev.id}
                    <span class="rounded bg-amber-500 px-1.5 py-0.5 text-xs font-semibold text-stone-950" role="status">Created</span>
                  {/if}
                </div>
                <p class="mt-1 text-sm text-stone-300">
                  Room <code class="font-semibold text-amber-300">{ev.id}</code>
                  · {ev.photoCount} {ev.photoCount === 1 ? 'photo' : 'photos'}{#if ev.hiddenCount > 0}, {ev.hiddenCount} hidden{/if}
                  · created {relativeTime(ev.createdAt, now)}
                </p>
                <p class="mt-1 text-xs text-stone-400 truncate">{origin}/{ev.id}</p>
              </div>
              <div class="flex flex-wrap gap-2">
                <button type="button" class="btn-ghost" onclick={() => copyLink(ev.id)}>
                  {#if copiedRoom === ev.id}
                    <Check class="w-4 h-4" aria-hidden="true" /><span>Copied</span>
                  {:else}
                    <Copy class="w-4 h-4" aria-hidden="true" /><span>Copy guest link</span>
                  {/if}
                </button>
                <a href="/{ev.id}/wall" target="_blank" rel="noopener" class="btn-ghost">
                  <Tv class="w-4 h-4" aria-hidden="true" /><span>Wall</span>
                  <ExternalLink class="w-3.5 h-3.5 opacity-70" aria-hidden="true" />
                  <span class="sr-only">(opens in a new tab)</span>
                </a>
                <a href="/admin/{ev.id}" class="btn-primary">
                  <Settings class="w-4 h-4" aria-hidden="true" /><span>Manage</span>
                </a>
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  </main>
</div>

<style>
  .panel {
    border-radius: 0.75rem;
    border: 1px solid rgba(255, 255, 255, 0.1);
    background: rgba(28, 25, 23, 0.7);
    padding: 1.25rem;
  }

  .event-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem 1rem;
    border-radius: 0.75rem;
    border: 1px solid rgba(255, 255, 255, 0.1);
    background: rgba(28, 25, 23, 0.7);
    padding: 1rem 1.25rem;
  }

  .event-row-new {
    border-color: #f59e0b;
  }

  .label {
    display: block;
    font-size: 0.875rem;
    font-weight: 600;
    color: #e7e5e4;
    margin-bottom: 0.375rem;
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

  .field-error {
    margin-top: 0.375rem;
    font-size: 0.8rem;
    color: #fde68a;
  }

  .btn-primary,
  .btn-ghost {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.45rem;
    min-height: 44px;
    padding: 0 1rem;
    border-radius: 0.5rem;
    font-weight: 600;
    font-size: 0.875rem;
    text-decoration: none;
    transition: background-color 120ms ease;
  }

  .btn-primary {
    background: #f59e0b;
    color: #1c1917;
  }

  .btn-primary:hover:not(:disabled) {
    background: #fbbf24;
  }

  .btn-ghost {
    border: 1px solid rgba(250, 246, 240, 0.22);
    background: rgba(28, 25, 23, 0.6);
    color: #faf6f0;
  }

  .btn-ghost:hover {
    background: #292524;
    border-color: rgba(250, 246, 240, 0.45);
  }

  .btn-primary:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .btn-primary:focus-visible,
  .btn-ghost:focus-visible {
    outline: 3px solid #fbbf24;
    outline-offset: 2px;
  }

  @media (prefers-reduced-motion: reduce) {
    .btn-primary,
    .btn-ghost {
      transition: none;
    }
  }
</style>
