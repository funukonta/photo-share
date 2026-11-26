<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, fetchConfig, isRoomNotFound, normalizeRoomCode } from '../lib/api';
  import { LoaderCircle } from 'lucide-svelte';

  let { notFound = '' }: { notFound?: string } = $props();

  let code = $state('');
  let checking = $state(false);
  let error = $state('');
  let inputEl = $state<HTMLInputElement | null>(null);

  async function join(e: SubmitEvent) {
    e.preventDefault();
    if (checking) return;
    const room = normalizeRoomCode(code);
    if (!room) {
      error = 'Room codes are 6 letters or numbers, like k7p2qx.';
      inputEl?.focus();
      return;
    }
    checking = true;
    error = '';
    try {
      await fetchConfig(room); // confirm it exists before leaving the page
      window.location.assign(`/${room}`);
    } catch (err) {
      if (isRoomNotFound(err)) {
        error = `There is no event with code ${room}. Check the code on the big screen.`;
      } else if (err instanceof ApiError) {
        error = err.message;
      } else {
        error = 'The server could not be reached. Check your connection and try again.';
      }
      checking = false;
      inputEl?.focus();
    }
  }

  onMount(() => {
    if (notFound) error = `There is no event at "/${notFound}". Type the room code from the big screen.`;
  });
</script>

<main class="min-h-dvh darkroom-bg text-[#faf6f0] flex items-center justify-center p-6">
  <div class="w-full max-w-sm">
    <div class="join-card polaroid-paper">
      <h1 class="font-display text-2xl font-bold text-stone-900">Join the photo wall</h1>
      <p class="mt-1.5 text-sm text-stone-700">Scan the QR code on the big screen, or type the room code shown under it.</p>

      <form class="mt-5" onsubmit={join} novalidate>
        <label for="room-code" class="block text-sm font-semibold text-stone-800">Room code</label>
        <input
          id="room-code"
          bind:this={inputEl}
          bind:value={code}
          inputmode="text"
          autocapitalize="off"
          autocomplete="off"
          autocorrect="off"
          spellcheck="false"
          maxlength="6"
          placeholder="k7p2qx"
          enterkeyhint="go"
          aria-invalid={error ? 'true' : undefined}
          aria-describedby={error ? 'room-error' : undefined}
          class="code-input"
        />
        {#if error}
          <p id="room-error" class="mt-2 text-sm text-red-800" role="alert">{error}</p>
        {/if}
        <button type="submit" class="join-btn mt-4 w-full" disabled={checking}>
          {#if checking}
            <LoaderCircle class="w-4 h-4 animate-spin" aria-hidden="true" />
            <span>Checking</span>
          {:else}
            <span>Join</span>
          {/if}
        </button>
      </form>
    </div>
    <p class="mt-6 text-center text-xs text-stone-400">
      Hosting? <a href="/admin" class="underline underline-offset-4 text-stone-200 hover:text-amber-300">Open the host panel</a>
    </p>
  </div>
</main>

<style>
  /* The join card is a Polaroid, the same object guests are about to make. */
  .join-card {
    border-radius: 0.375rem;
    padding: 1.5rem;
    transform: rotate(-1deg);
  }

  .code-input {
    display: block;
    width: 100%;
    margin-top: 0.375rem;
    min-height: 56px;
    border-radius: 0.5rem;
    border: 2px solid #a8a29e;
    background: #fff;
    color: #1c1917;
    padding: 0.5rem 0.75rem;
    font-family: var(--font-display);
    font-size: 1.75rem;
    font-weight: 700;
    letter-spacing: 0.25em;
    text-transform: lowercase;
    text-align: center;
  }

  .code-input::placeholder {
    color: #a8a29e;
  }

  .code-input:focus-visible {
    outline: 3px solid #d97706;
    outline-offset: 2px;
    border-color: #d97706;
  }

  .code-input[aria-invalid='true'] {
    border-color: #b91c1c;
  }

  .join-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.45rem;
    min-height: 48px;
    border-radius: 0.5rem;
    background: #1c1917;
    color: #faf6f0;
    font-weight: 700;
    transition: background-color 120ms ease;
  }

  .join-btn:hover:not(:disabled) {
    background: #44403c;
  }

  .join-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .join-btn:focus-visible {
    outline: 3px solid #d97706;
    outline-offset: 2px;
  }

  @media (prefers-reduced-motion: reduce) {
    .join-btn {
      transition: none;
    }
  }
</style>
