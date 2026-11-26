<script lang="ts">
  import QRCode from 'qrcode';
  import type { UploadTarget } from '../lib/wall';

  interface Props {
    target: UploadTarget;
    room: string;
    count: number;
    countKnown: boolean;
    maxPerPerson: number;
    requiresEventCode: boolean;
  }

  let { target, room, count, countKnown, maxPerPerson, requiresEventCode }: Props = $props();

  let qrSvg = $state('');
  let qrError = $state(false);

  $effect(() => {
    const url = target.url;
    let cancelled = false;
    QRCode.toString(url, {
      type: 'svg',
      errorCorrectionLevel: 'M',
      margin: 1,
      color: { dark: '#141210', light: '#faf6f0' },
    })
      .then((svg) => {
        if (cancelled) return;
        qrSvg = svg;
        qrError = false;
      })
      .catch(() => {
        if (!cancelled) qrError = true;
      });
    return () => {
      cancelled = true;
    };
  });

  let missingCode = $derived(requiresEventCode && !target.hasEventCode);
</script>

<section class="qr-card" aria-labelledby="qr-heading">
  <div class="frame">
  <div class="pin polaroid-paper">
    <span class="tape" aria-hidden="true"></span>
    <div class="code" role="img" aria-label="QR code linking to {target.displayUrl}">
      {#if qrSvg}
        {@html qrSvg}
      {:else if qrError}
        <p class="code-fallback">QR code could not be drawn. Type the address below instead.</p>
      {:else}
        <p class="code-fallback">Drawing QR code</p>
      {/if}
    </div>
    <h2 id="qr-heading" class="font-handwriting scan">Scan to pin your photos</h2>
  </div>
  </div>

  <p class="url">{target.displayUrl}</p>
  <p class="room-code">Room code <span class="font-display">{room}</span></p>

  {#if missingCode}
    <p class="warn" role="alert">
      This event needs an access code, and this QR does not carry one. Reopen the wall as
      <code>/{room}/wall?code=YOUR_CODE</code>.
    </p>
  {:else if target.pointsToLocalhost}
    <p class="warn" role="alert">
      Phones cannot reach <code>localhost</code>. Open this wall through the server's network address, or add
      <code>?url=http://YOUR-IP:8080</code> to this page.
    </p>
  {/if}

  <div class="counter" aria-live="polite">
    {#if countKnown}
      <span class="count font-display">{count}</span>
      <span class="count-label">{count === 1 ? 'memory' : 'memories'} pinned</span>
    {:else}
      <span class="count-label">Counting memories</span>
    {/if}
  </div>
  <p class="rule">Up to {maxPerPerson} photos each, no sign-up.</p>
</section>

<style>
  .qr-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 0.9rem;
  }

  .frame {
    container-type: inline-size;
    /* Capped by height too, so the whole card (QR, address, counter) fits on a 768px-tall laptop. */
    width: min(100%, 34dvh);
  }

  .pin {
    position: relative;
    width: 100%;
    padding: 6cqw 6cqw 3cqw;
    border-radius: 0.6cqw;
    color: #1c1917;
    transform: rotate(-1.5deg);
  }

  .tape {
    position: absolute;
    top: -3.5cqw;
    left: 50%;
    width: 36cqw;
    height: 8cqw;
    transform: translateX(-50%) rotate(2deg);
    background: rgba(240, 228, 196, 0.62);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.18);
  }

  .code {
    aspect-ratio: 1 / 1;
    display: grid;
    place-items: center;
  }

  .code :global(svg) {
    display: block;
    width: 100%;
    height: 100%;
  }

  .code-fallback {
    margin: 0;
    padding: 0 8cqw;
    font-size: 6cqw;
    color: #44403c;
  }

  .scan {
    margin: 2cqw 0 0;
    font-size: 9cqw;
    line-height: 1.05;
    font-weight: 700;
    color: #292524;
  }

  .room-code {
    margin: -0.4rem 0 0;
    font-size: clamp(0.9rem, 1vw, 1.15rem);
    color: #d6d3d1;
  }

  .room-code span {
    margin-left: 0.35rem;
    font-weight: 700;
    letter-spacing: 0.18em;
    color: #faf6f0;
  }

  .url {
    margin: 0;
    max-width: 100%;
    font-family: var(--font-display);
    font-size: clamp(0.95rem, 1.15vw, 1.35rem);
    font-weight: 600;
    color: #faf6f0;
    overflow-wrap: anywhere;
  }

  .warn {
    margin: 0;
    padding: 0.6rem 0.75rem;
    border-radius: 0.5rem;
    border: 1px solid rgba(251, 191, 36, 0.45);
    background: rgba(120, 53, 15, 0.35);
    color: #fde68a;
    font-size: 0.85rem;
    line-height: 1.4;
    text-align: left;
  }

  .warn code {
    font-size: 0.8rem;
    color: #fef3c7;
    overflow-wrap: anywhere;
  }

  .counter {
    display: flex;
    flex-direction: column;
    align-items: center;
    margin-top: 0.4rem;
  }

  .count {
    font-size: clamp(2.5rem, 4.4vw, 5rem);
    font-weight: 700;
    line-height: 1;
    color: #fbbf24;
    font-variant-numeric: tabular-nums;
  }

  .count-label {
    font-size: clamp(0.95rem, 1.1vw, 1.25rem);
    color: #d6d3d1;
  }

  .rule {
    margin: 0;
    font-size: clamp(0.85rem, 0.95vw, 1.05rem);
    color: #a8a29e;
  }
</style>
