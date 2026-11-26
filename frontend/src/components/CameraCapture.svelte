<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { Camera, RefreshCw, SwitchCamera, X, Check, RotateCcw } from 'lucide-svelte';
  import { inPageCameraSupported } from '../lib/camera';

  interface Props {
    /** Called with a square JPEG of exactly what the viewfinder showed. */
    onCapture: (file: File) => void;
    onCancel: () => void;
    /** Called when the in-page camera can't run; the parent opens the phone's own camera app instead. */
    onFallback: () => void;
  }

  let { onCapture, onCancel, onFallback }: Props = $props();

  type Facing = 'user' | 'environment';
  type CamState = 'starting' | 'live' | 'review' | 'error';

  const OUTPUT_SIZE = 1600; // matches the upload compressor's max dimension

  let videoEl = $state<HTMLVideoElement | null>(null);
  let stream: MediaStream | null = null;
  let camState = $state<CamState>('starting');
  let errorText = $state('');
  let facing = $state<Facing>('user');
  let canFlip = $state(false);
  let flash = $state(false);
  let shot = $state<{ file: File; url: string } | null>(null);

  function stopStream() {
    stream?.getTracks().forEach((t) => t.stop());
    stream = null;
    if (videoEl) videoEl.srcObject = null;
  }

  async function start(nextFacing: Facing) {
    stopStream();
    camState = 'starting';
    errorText = '';
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: false,
        video: {
          facingMode: { ideal: nextFacing },
          width: { ideal: 1920 },
          height: { ideal: 1920 },
        },
      });
      facing = nextFacing;
      if (videoEl) {
        videoEl.srcObject = stream;
        await videoEl.play().catch(() => {});
      }
      camState = 'live';
      const devices = await navigator.mediaDevices.enumerateDevices().catch(() => []);
      canFlip = devices.filter((d) => d.kind === 'videoinput').length > 1;
    } catch (err) {
      camState = 'error';
      const name = err instanceof DOMException ? err.name : '';
      if (name === 'NotAllowedError' || name === 'SecurityError') {
        errorText =
          'Camera access was blocked. Allow the camera for this site in your browser settings, or open your phone camera instead.';
      } else if (name === 'NotFoundError' || name === 'OverconstrainedError') {
        errorText = 'No camera was found on this device.';
      } else if (name === 'NotReadableError') {
        errorText = 'Another app is using the camera. Close it and try again, or open your phone camera instead.';
      } else {
        errorText = 'The camera could not start here. Open your phone camera instead.';
      }
    }
  }

  function flip() {
    if (camState !== 'live') return;
    start(facing === 'user' ? 'environment' : 'user');
  }

  async function takePhoto() {
    if (!videoEl || camState !== 'live') return;
    const vw = videoEl.videoWidth;
    const vh = videoEl.videoHeight;
    if (!vw || !vh) return;

    // Centre square crop: the same area the object-cover viewfinder shows.
    const side = Math.min(vw, vh);
    const sx = (vw - side) / 2;
    const sy = (vh - side) / 2;
    const out = Math.min(side, OUTPUT_SIZE);

    const canvas = document.createElement('canvas');
    canvas.width = out;
    canvas.height = out;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    if (facing === 'user') {
      // The selfie preview is mirrored, so save it mirrored too: what you saw is what goes on the wall.
      ctx.translate(out, 0);
      ctx.scale(-1, 1);
    }
    ctx.drawImage(videoEl, sx, sy, side, side, 0, 0, out, out);

    flash = true;
    setTimeout(() => (flash = false), 180);

    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.86));
    if (!blob) return;
    const file = new File([blob], `camera-${Date.now()}.jpg`, { type: 'image/jpeg', lastModified: Date.now() });
    shot = { file, url: URL.createObjectURL(file) };
    camState = 'review';
    stopStream(); // camera light off while reviewing
  }

  function retake() {
    if (shot) URL.revokeObjectURL(shot.url);
    shot = null;
    start(facing);
  }

  function usePhoto() {
    if (!shot) return;
    const file = shot.file;
    URL.revokeObjectURL(shot.url);
    shot = null;
    onCapture(file);
  }

  function cancel() {
    stopStream();
    onCancel();
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') cancel();
  }

  onMount(() => {
    // The parent only mounts this when inPageCameraSupported() is true; guard anyway.
    if (!inPageCameraSupported()) {
      camState = 'error';
      errorText = 'The camera could not start here. Open your phone camera instead.';
      return;
    }
    start('user');
    // Release the camera when the tab is hidden (lock screen, app switch); resume on return.
    const onVisibility = () => {
      if (document.hidden) stopStream();
      else if (camState === 'live' || camState === 'starting') start(facing);
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  });

  onDestroy(() => {
    stopStream();
    if (shot) URL.revokeObjectURL(shot.url);
  });
</script>

<svelte:window onkeydown={onKeydown} />

<div class="space-y-3">
  <div class="relative aspect-square w-full overflow-hidden rounded-lg bg-stone-950 border border-stone-800">
    <!-- svelte-ignore a11y_media_has_caption -->
    <video
      bind:this={videoEl}
      playsinline
      muted
      autoplay
      class="absolute inset-0 h-full w-full object-cover {facing === 'user' ? 'mirror' : ''} {camState === 'live' ? '' : 'invisible'}"
    ></video>

    {#if camState === 'review' && shot}
      <img src={shot.url} alt="Preview of the shot you just took" class="absolute inset-0 h-full w-full object-cover" width="1600" height="1600" />
    {/if}

    {#if camState === 'starting'}
      <div class="absolute inset-0 flex flex-col items-center justify-center gap-2 text-stone-300" role="status">
        <RefreshCw class="w-7 h-7 animate-spin text-amber-400" aria-hidden="true" />
        <p class="text-sm">Starting the camera. Allow access if your phone asks.</p>
      </div>
    {:else if camState === 'error'}
      <div class="absolute inset-0 flex flex-col items-center justify-center gap-4 p-6 text-center" role="alert">
        <p class="text-sm text-stone-200 leading-relaxed">{errorText}</p>
        <div class="flex flex-wrap justify-center gap-2">
          <button type="button" class="cam-btn cam-btn-primary" onclick={onFallback}>
            <Camera class="w-4 h-4" aria-hidden="true" />
            <span>Open phone camera</span>
          </button>
          <button type="button" class="cam-btn" onclick={() => start(facing)}>Try again</button>
        </div>
      </div>
    {/if}

    {#if flash}
      <div class="absolute inset-0 bg-white/80 pointer-events-none" aria-hidden="true"></div>
    {/if}

    {#if camState === 'live' && canFlip}
      <button type="button" class="cam-icon absolute top-3 right-3" onclick={flip} aria-label="Switch to {facing === 'user' ? 'back' : 'front'} camera">
        <SwitchCamera class="w-5 h-5" aria-hidden="true" />
      </button>
    {/if}
  </div>

  {#if camState === 'review'}
    <div class="grid grid-cols-2 gap-2">
      <button type="button" class="cam-btn" onclick={retake}>
        <RotateCcw class="w-4 h-4" aria-hidden="true" />
        <span>Retake</span>
      </button>
      <button type="button" class="cam-btn cam-btn-primary" onclick={usePhoto}>
        <Check class="w-4 h-4" aria-hidden="true" />
        <span>Use this photo</span>
      </button>
    </div>
  {:else if camState !== 'error'}
    <div class="grid grid-cols-[1fr_auto_1fr] items-center gap-2">
      <button type="button" class="cam-btn justify-self-start" onclick={cancel}>
        <X class="w-4 h-4" aria-hidden="true" />
        <span>Cancel</span>
      </button>
      <button
        type="button"
        class="shutter"
        onclick={takePhoto}
        disabled={camState !== 'live'}
        aria-label="Take photo"
      ></button>
      <span></span>
    </div>
  {:else}
    <button type="button" class="cam-btn" onclick={cancel}>
      <X class="w-4 h-4" aria-hidden="true" />
      <span>Back</span>
    </button>
  {/if}
</div>

<style>
  .mirror {
    transform: scaleX(-1);
  }

  .cam-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.45rem;
    min-height: 44px;
    padding: 0 1rem;
    border-radius: 0.75rem;
    border: 1px solid #57534e;
    background: #1c1917;
    color: #faf6f0;
    font-size: 0.875rem;
    font-weight: 600;
  }

  .cam-btn:hover {
    background: #292524;
  }

  .cam-btn-primary {
    background: #f59e0b;
    border-color: #f59e0b;
    color: #1c1917;
  }

  .cam-btn-primary:hover {
    background: #fbbf24;
  }

  .cam-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 44px;
    height: 44px;
    border-radius: 999px;
    background: rgba(12, 10, 9, 0.7);
    color: #faf6f0;
    border: 1px solid rgba(250, 246, 240, 0.25);
  }

  /* The shutter is the one big target on this screen: a classic white camera release. */
  .shutter {
    width: 72px;
    height: 72px;
    border-radius: 999px;
    background: #faf6f0;
    border: 5px solid #1c1917;
    box-shadow: 0 0 0 3px #faf6f0;
    transition: transform 100ms ease;
  }

  .shutter:active:not(:disabled) {
    transform: scale(0.92);
  }

  .shutter:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .cam-btn:focus-visible,
  .cam-icon:focus-visible,
  .shutter:focus-visible {
    outline: 3px solid #fbbf24;
    outline-offset: 3px;
  }

  @media (prefers-reduced-motion: reduce) {
    .shutter {
      transition: none;
    }
  }
</style>
