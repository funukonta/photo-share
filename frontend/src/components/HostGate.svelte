<script lang="ts" module>
  import { getContext } from 'svelte';

  export interface HostSession {
    /** Call when an admin request returns 401: shows the PIN screen with a message. */
    expire: () => void;
    logout: () => Promise<void>;
  }

  const KEY = Symbol('host-session');

  export function useHostSession(): HostSession {
    return getContext<HostSession>(KEY);
  }
</script>

<script lang="ts">
  import { onMount, setContext, type Snippet } from 'svelte';
  import { LoaderCircle } from 'lucide-svelte';
  import { ApiError, adminHasSession, adminLogin, adminLogout } from '../lib/api';

  let { children }: { children: Snippet } = $props();

  type Phase = 'checking' | 'login' | 'ready' | 'unreachable';
  let phase = $state<Phase>('checking');
  let pin = $state('');
  let loginError = $state('');
  let loggingIn = $state(false);
  let lockedUntil = $state(0);
  let now = $state(Date.now());
  let lockSeconds = $derived(Math.max(0, Math.ceil((lockedUntil - now) / 1000)));

  setContext<HostSession>(KEY, {
    expire() {
      phase = 'login';
      loginError = 'Your host session ended. Enter the PIN again.';
    },
    async logout() {
      try {
        await adminLogout();
      } catch {
        // The cookie expires on its own; the screen still returns to the PIN form.
      }
      phase = 'login';
      loginError = '';
    },
  });

  async function submitLogin(e: SubmitEvent) {
    e.preventDefault();
    if (!pin.trim() || loggingIn || lockSeconds > 0) return;
    loggingIn = true;
    loginError = '';
    try {
      await adminLogin(pin.trim());
      pin = '';
      phase = 'ready';
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        const retry = Number(err.data?.retryAfter) || 60;
        now = Date.now();
        lockedUntil = now + retry * 1000;
        loginError = 'Too many wrong PINs.';
      } else if (err instanceof ApiError && err.status === 401) {
        loginError = 'That PIN is not right.';
      } else if (err instanceof TypeError) {
        loginError = 'The server could not be reached. Check the connection and try again.';
      } else {
        loginError = err instanceof Error ? err.message : 'Could not sign in.';
      }
    } finally {
      loggingIn = false;
    }
  }

  onMount(() => {
    adminHasSession()
      .then((ok) => {
        phase = ok ? 'ready' : 'login';
      })
      .catch(() => {
        phase = 'unreachable';
      });
    const clock = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(clock);
  });
</script>

{#if phase === 'ready'}
  {@render children()}
{:else if phase === 'checking'}
  <div class="min-h-dvh darkroom-bg flex items-center justify-center p-6" role="status">
    <LoaderCircle class="w-7 h-7 text-amber-400 animate-spin" aria-hidden="true" />
    <span class="sr-only">Checking host session</span>
  </div>
{:else if phase === 'unreachable'}
  <main class="min-h-dvh darkroom-bg flex items-center justify-center p-6">
    <div class="w-full max-w-sm rounded-xl border border-white/10 bg-stone-900/80 p-6 text-center" role="alert">
      <h1 class="font-display text-xl font-bold">The server could not be reached</h1>
      <p class="mt-2 text-sm text-stone-300">Check that the backend is running, then reload this page.</p>
      <button type="button" class="gate-btn mt-5" onclick={() => location.reload()}>Reload</button>
    </div>
  </main>
{:else}
  <main class="min-h-dvh darkroom-bg flex items-center justify-center p-6">
    <form class="w-full max-w-sm rounded-xl border border-white/10 bg-stone-900/80 p-6 sm:p-8" onsubmit={submitLogin} novalidate>
      <h1 class="font-display text-2xl font-bold">Host panel</h1>
      <p class="mt-1.5 text-sm text-stone-300">Enter the host PIN to create events and manage their walls.</p>

      <label for="host-pin" class="block mt-6 text-sm font-semibold text-stone-200">Host PIN</label>
      <input
        id="host-pin"
        type="password"
        inputmode="numeric"
        autocomplete="current-password"
        class="mt-1.5 block w-full min-h-11 rounded-lg border border-stone-600 bg-stone-950 px-3 py-2 text-lg tracking-[0.3em] text-[#faf6f0] focus-visible:outline-3 focus-visible:outline-amber-400 disabled:opacity-55"
        bind:value={pin}
        disabled={lockSeconds > 0}
        aria-invalid={loginError ? 'true' : undefined}
        aria-describedby={loginError ? 'pin-error' : undefined}
      />

      {#if loginError}
        <p id="pin-error" class="mt-2 text-sm text-amber-200" role="alert">
          {loginError}
          {#if lockSeconds > 0}Try again in {lockSeconds}s.{/if}
        </p>
      {/if}

      <button type="submit" class="gate-btn w-full mt-5" disabled={!pin.trim() || loggingIn || lockSeconds > 0}>
        {#if loggingIn}
          <LoaderCircle class="w-4 h-4 animate-spin" aria-hidden="true" />
          <span>Checking PIN</span>
        {:else}
          <span>Unlock host panel</span>
        {/if}
      </button>
    </form>
  </main>
{/if}

<style>
  .gate-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.45rem;
    min-height: 44px;
    padding: 0 1.1rem;
    border-radius: 0.5rem;
    background: #f59e0b;
    color: #1c1917;
    font-weight: 600;
    font-size: 0.875rem;
  }

  .gate-btn:hover:not(:disabled) {
    background: #fbbf24;
  }

  .gate-btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .gate-btn:focus-visible {
    outline: 3px solid #fbbf24;
    outline-offset: 2px;
  }
</style>
