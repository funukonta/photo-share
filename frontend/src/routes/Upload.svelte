<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import {
    ALLOWED_REACTION_EMOJIS,
    ApiError,
    isRoomNotFound,
    fetchConfig,
    fetchMe,
    getStoredEventCode,
    getStoredNickname,
    sendReaction,
    setStoredEventCode,
    setStoredNickname,
    uploadPhoto,
    type MeResponse,
    type Photo,
    type PublicConfigResponse,
  } from '../lib/api';
  import { compressImageFile, formatBytes, type CompressionResult } from '../lib/compress';
  import { inPageCameraSupported } from '../lib/camera';
  import CameraCapture from '../components/CameraCapture.svelte';
  import { connectSSE, type SSEClient } from '../lib/sse';
  import {
    Camera,
    CheckCircle2,
    Sparkles,
    Tv,
    RefreshCw,
    ImagePlus,
    KeyRound,
    AlertCircle,
    Film,
    Send,
    X,
    Clock,
    ArrowLeft,
    Pencil,
  } from 'lucide-svelte';

  const MAX_NICKNAME_LENGTH = 40;
  const MAX_CAPTION_LENGTH = 100;

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

  let me = $state<MeResponse>({
    deviceId: '',
    deviceToken: '',
    uploadedCount: 0,
    remainingCount: 5,
    maxPhotosPerPerson: 5,
    photos: [],
  });

  let loadingInitial = $state(true);
  let loadError = $state('');

  // Form & composer state
  let nickname = $state(getStoredNickname());
  let caption = $state('');
  // The page is keyed by room in App.svelte, so room never changes for this instance.
  // svelte-ignore state_referenced_locally
  let eventCode = $state(getStoredEventCode(room));
  let showEventCodeEditor = $state(false);

  // Guided flow: name -> camera or upload -> caption. Returning guests (nickname saved) start at "source".
  type Step = 'name' | 'source' | 'camera' | 'caption';
  let step = $state<Step>(getStoredNickname().trim() ? 'source' : 'name');
  let nameDraft = $state(getStoredNickname());
  let nameError = $state('');
  let stepHeadingEl = $state<HTMLElement | null>(null);
  let cameraInputEl = $state<HTMLInputElement | null>(null);

  // Selected image & compression state
  let fileInputEl = $state<HTMLInputElement | null>(null);
  let isCompressing = $state(false);
  let compression = $state<CompressionResult | null>(null);

  // Upload & feedback state
  let isUploading = $state(false);
  let uploadError = $state('');
  let uploadSuccessToast = $state<{ visible: boolean; caption: string }>({
    visible: false,
    caption: '',
  });
  let toastTimer: ReturnType<typeof setTimeout> | null = null;

  // Emoji reaction bar state
  interface BurstItem {
    id: number;
    emoji: string;
  }
  let reactionBursts = $state<BurstItem[]>([]);
  let reactionCooldown = $state(false);
  let lastTappedEmoji = $state('');
  let burstCounter = 0;

  // Expanded memory modal preview
  let activeModalPhoto = $state<Photo | null>(null);

  let sseClient: SSEClient | null = null;

  // Derived states
  let maxQuota = $derived(me.maxPhotosPerPerson || config.maxPhotosPerPerson || 5);
  let remainingCount = $derived(me.remainingCount);
  let uploadedCount = $derived(me.uploadedCount);
  let isQuotaFull = $derived(!loadingInitial && remainingCount <= 0);
  let needsEventCode = $derived(config.requiresEventCode && !eventCode.trim());

  let slotIndices = $derived(Array.from({ length: maxQuota }, (_, i) => i));

  let captionLength = $derived(Array.from(caption).length);
  const STEPS = [
    { key: 'name', label: 'Name' },
    { key: 'photo', label: 'Photo' },
    { key: 'caption', label: 'Caption' },
  ] as const;
  let currentStepIndex = $derived(step === 'name' ? 0 : step === 'caption' ? 2 : 1);

  let canSubmit = $derived(
    !isUploading &&
      !isCompressing &&
      !isQuotaFull &&
      Boolean(compression?.file) &&
      Boolean(nickname.trim()) &&
      (!config.requiresEventCode || Boolean(eventCode.trim()))
  );

  function deterministicTilt(id: string, index: number): number {
    let hash = index * 17;
    for (let i = 0; i < id.length; i++) {
      hash = (hash * 31 + id.charCodeAt(i)) % 997;
    }
    const tilts = [-2.2, 1.8, -1.4, 2.4, -1.8, 1.2];
    return tilts[Math.abs(hash) % tilts.length];
  }

  function formatRelativeTime(iso: string): string {
    if (!iso) return 'Just now';
    const date = new Date(iso);
    const diffSec = Math.floor((Date.now() - date.getTime()) / 1000);
    if (Number.isNaN(diffSec) || diffSec < 45) return 'Just now';
    if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
    if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
    return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }

  async function loadInitialState() {
    loadingInitial = true;
    loadError = '';
    try {
      const [cfgRes, meRes] = await Promise.all([fetchConfig(room), fetchMe(room)]);
      config = cfgRes;
      me = {
        ...meRes,
        photos: meRes.photos || [],
      };
      if (!nickname && meRes.photos?.length > 0 && meRes.photos[0].nickname) {
        nickname = meRes.photos[0].nickname;
        nameDraft = nickname;
        setStoredNickname(nickname);
        if (step === 'name') step = 'source';
      }
    } catch (err) {
      if (isRoomNotFound(err)) {
        notFound = true;
        sseClient?.close();
        return;
      }
      loadError = err instanceof Error ? err.message : 'Unable to connect to the photo wall server.';
    } finally {
      loadingInitial = false;
    }
  }

  function handleEventCodeInput(e: Event) {
    const val = (e.currentTarget as HTMLInputElement).value;
    eventCode = val;
    setStoredEventCode(room, val);
  }

  async function goTo(next: Step) {
    step = next;
    await tick();
    // Move focus to the new step's heading so keyboard and screen-reader users land in the right place.
    stepHeadingEl?.focus();
  }

  function confirmNickname(e: SubmitEvent) {
    e.preventDefault();
    const clean = nameDraft.trim();
    if (!clean) {
      nameError = 'Type the name your classmates know you by.';
      return;
    }
    nameError = '';
    nickname = clean;
    setStoredNickname(clean);
    goTo(compression ? 'caption' : 'source');
  }

  function editNickname() {
    nameDraft = nickname;
    goTo('name');
  }

  function chooseCamera() {
    if (isQuotaFull) return;
    uploadError = '';
    if (inPageCameraSupported()) {
      goTo('camera');
    } else {
      // No in-page camera (plain HTTP, some in-app browsers): open the phone's camera app directly.
      openPhoneCamera();
    }
  }

  function openPhoneCamera() {
    cameraInputEl?.click();
  }

  function chooseUpload() {
    if (isQuotaFull) return;
    uploadError = '';
    fileInputEl?.click();
  }

  function backToSource() {
    clearSelectedPhoto();
    goTo('source');
  }

  async function processPhoto(rawFile: File) {
    uploadError = '';
    isCompressing = true;
    if (compression?.previewUrl) {
      URL.revokeObjectURL(compression.previewUrl);
    }
    compression = null;
    goTo('caption');
    try {
      compression = await compressImageFile(rawFile, { maxDimension: 1600, quality: 0.84 });
    } catch {
      uploadError = 'Could not read that photo. Try another one.';
      goTo('source');
    } finally {
      isCompressing = false;
    }
  }

  function handleCameraCapture(file: File) {
    processPhoto(file);
  }

  function clearSelectedPhoto() {
    if (compression?.previewUrl) {
      URL.revokeObjectURL(compression.previewUrl);
    }
    compression = null;
    uploadError = '';
    if (fileInputEl) {
      fileInputEl.value = '';
    }
  }

  async function handleFileSelected(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const rawFile = input.files?.[0];
    input.value = ''; // picking the same file twice should still fire change
    if (!rawFile) return;
    await processPhoto(rawFile);
  }

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    if (!compression?.file || isUploading || isQuotaFull) return;

    const cleanNickname = nickname.trim();
    if (!cleanNickname) {
      uploadError = 'Please enter your nickname so classmates know who snapped this memory!';
      return;
    }

    if (config.requiresEventCode && !eventCode.trim()) {
      uploadError = 'Please enter the venue Event Code from the projector screen.';
      showEventCodeEditor = true;
      return;
    }

    isUploading = true;
    uploadError = '';
    setStoredNickname(cleanNickname);
    if (eventCode.trim()) {
      setStoredEventCode(room, eventCode.trim());
    }

    try {
      const res = await uploadPhoto(room, {
        file: compression.file,
        filename: compression.file.name,
        nickname: cleanNickname,
        caption: caption.trim(),
        eventCode: eventCode.trim(),
      });

      const uploadedCaption = res.photo.caption || 'Sent to the Big Screen!';

      // Update local state & quota immediately
      const existingFiltered = (me.photos || []).filter((p) => p.id !== res.photo.id);
      me = {
        ...me,
        deviceToken: res.deviceToken || me.deviceToken,
        uploadedCount: res.uploadedCount,
        remainingCount: res.remainingCount,
        maxPhotosPerPerson: res.maxPhotosPerPerson,
        photos: [res.photo, ...existingFiltered],
      };

      // Reset composer for next photo
      clearSelectedPhoto();
      caption = '';
      goTo('source');

      // Show celebratory toast
      if (toastTimer) clearTimeout(toastTimer);
      uploadSuccessToast = {
        visible: true,
        caption: uploadedCaption,
      };
      toastTimer = setTimeout(() => {
        uploadSuccessToast = { visible: false, caption: '' };
      }, 4200);
    } catch (err) {
      if (err instanceof ApiError) {
        uploadError = err.message;
        if (err.code === 'EVENT_CODE_INVALID') {
          showEventCodeEditor = true;
        }
        if (err.code === 'DEVICE_QUOTA_EXCEEDED' || err.code === 'UPLOADS_CLOSED') {
          await loadInitialState();
        }
        if (isRoomNotFound(err)) notFound = true;
      } else {
        uploadError = err instanceof Error ? err.message : 'Failed to upload photo. Please try again.';
      }
    } finally {
      isUploading = false;
    }
  }

  async function handleReactionTap(emoji: string) {
    if (reactionCooldown) return;

    reactionCooldown = true;
    lastTappedEmoji = emoji;

    // Trigger local floating burst animation immediately
    const id = ++burstCounter;
    reactionBursts = [...reactionBursts, { id, emoji }];
    setTimeout(() => {
      reactionBursts = reactionBursts.filter((b) => b.id !== id);
    }, 950);

    setTimeout(() => {
      reactionCooldown = false;
      lastTappedEmoji = '';
    }, 360);

    try {
      await sendReaction(room, emoji, nickname.trim() || 'Classmate');
    } catch {
      // Ignore transient network hiccup on emoji tap
    }
  }

  onMount(() => {
    loadInitialState();

    sseClient = connectSSE(room, {
      onRoomDeleted: () => {
        notFound = true;
        sseClient?.close();
      },
      onConfigUpdated: (updated) => {
        config = {
          ...config,
          ...updated,
        };
        if (updated.maxPhotosPerPerson) {
          const newMax = updated.maxPhotosPerPerson;
          const newRem = Math.max(0, newMax - me.uploadedCount);
          me = {
            ...me,
            maxPhotosPerPerson: newMax,
            remainingCount: newRem,
          };
        }
      },
      onPhotoDeleted: (payload) => {
        const deletedId = 'id' in payload ? payload.id : '';
        if (deletedId && me.photos.some((p) => p.id === deletedId)) {
          // Refresh /api/me so remaining quota & user strip stay accurate if host moderated a photo
          fetchMe(room)
            .then((fresh) => {
              me = { ...fresh, photos: fresh.photos || [] };
            })
            .catch(() => {});
        }
      },
    });
  });

  onDestroy(() => {
    sseClient?.close();
    if (compression?.previewUrl) {
      URL.revokeObjectURL(compression.previewUrl);
    }
    if (toastTimer) {
      clearTimeout(toastTimer);
    }
  });
</script>

{#if notFound}
  <main class="min-h-dvh darkroom-bg text-[#faf6f0] flex items-center justify-center p-6">
    <div class="w-full max-w-sm rounded-xl border border-white/10 bg-stone-900/80 p-6 text-center" role="alert">
      <h1 class="font-display text-xl font-bold">Event not found</h1>
      <p class="mt-2 text-sm text-stone-300">
        There is no event with room code <code class="text-[#faf6f0]">{room}</code>. Check the code on the big screen, or scan its QR code again.
      </p>
      <a href="/" class="mt-5 inline-flex min-h-11 items-center justify-center rounded-lg bg-amber-500 px-5 font-semibold text-stone-950 hover:bg-amber-400">Enter a room code</a>
    </div>
  </main>
{:else}
<div class="min-h-screen darkroom-bg text-[#faf6f0] pb-28">
  <!-- Top Analog Camera Bar -->
  <div class="sticky top-0 z-30 backdrop-blur-md bg-[#141210]/85 border-b border-stone-800/80">
    <div class="max-w-lg mx-auto px-4 py-2.5 flex items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <span class="inline-flex h-2.5 w-2.5 rounded-full bg-amber-500 shadow-[0_0_8px_#f59e0b]"></span>
        <span class="font-display text-xs uppercase tracking-widest text-stone-300 font-semibold">
          Polaroid Cam
        </span>
      </div>

      <div class="flex items-center gap-2">
        {#if config.requiresEventCode}
          <button
            type="button"
            onclick={() => (showEventCodeEditor = !showEventCodeEditor)}
            class="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border transition {eventCode.trim()
              ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300'
              : 'border-amber-500/40 bg-amber-500/15 text-amber-300'}"
          >
            <KeyRound class="w-3 h-3" />
            <span>{eventCode.trim() ? `Code: ${eventCode.trim()}` : 'Enter Event Code'}</span>
          </button>
        {/if}

        <a
          href="/{room}/wall"
          class="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-semibold bg-stone-800/90 hover:bg-stone-700 text-amber-300 border border-stone-700/80 transition"
        >
          <Tv class="w-3.5 h-3.5" />
          <span>Big Screen</span>
        </a>
      </div>
    </div>
  </div>

  <main class="max-w-lg mx-auto px-4 pt-5 space-y-6">
    <!-- Event Header -->
    <header class="text-center space-y-2">
      <h1 class="font-display text-2xl sm:text-3xl font-bold tracking-tight text-[#faf6f0] leading-tight">
        {config.title}
      </h1>
      <p class="text-sm text-stone-300/90 max-w-md mx-auto leading-relaxed">
        {config.subtitle}
      </p>
    </header>

    <!-- Film-Roll Quota Pill Badge -->
    <section aria-label="Personal Photo Quota">
      {#if isQuotaFull}
        <div
          class="rounded-2xl border border-amber-500/40 bg-gradient-to-br from-amber-500/15 via-stone-900/90 to-orange-600/15 p-5 text-center shadow-lg space-y-2"
        >
          <div class="w-11 h-11 rounded-full bg-amber-500/20 border border-amber-400/40 text-amber-300 flex items-center justify-center mx-auto">
            <Sparkles class="w-5 h-5" />
          </div>
          <h2 class="font-display text-lg font-bold text-amber-200">
            You’ve shared all {maxQuota} of your memories! 🎓
          </h2>
          <p class="text-xs text-stone-300 leading-relaxed max-w-sm mx-auto">
            Your Polaroids are up on the graduation wall! Keep cheering on your classmates below with live emoji reactions.
          </p>
          <div class="pt-1">
            <a
              href="/{room}/wall"
              class="inline-flex items-center gap-1.5 text-xs font-semibold text-amber-400 hover:text-amber-300 underline underline-offset-4"
            >
              <Tv class="w-3.5 h-3.5" />
              <span>Watch the Live Wall</span>
            </a>
          </div>
        </div>
      {:else}
        <div
          class="rounded-2xl border border-stone-800 bg-stone-900/80 px-4 py-3.5 shadow-inner flex items-center justify-between gap-3"
        >
          <div class="flex items-center gap-2.5">
            <div class="w-9 h-9 rounded-xl bg-amber-500/15 border border-amber-500/30 flex items-center justify-center text-amber-400 shrink-0">
              <Film class="w-4 h-4" />
            </div>
            <div>
              <p class="text-xs font-semibold uppercase tracking-wider text-stone-400">
                Personal Film Roll
              </p>
              <p class="text-sm font-bold text-[#faf6f0]">
                {remainingCount} of {maxQuota}
                {remainingCount === 1 ? 'photo' : 'photos'} remaining
              </p>
            </div>
          </div>

          <!-- Visual Film Roll Exposure Slots -->
          <div class="flex items-center gap-1.5" title="{uploadedCount} used, {remainingCount} remaining">
            {#each slotIndices as idx}
              {@const isRemainingSlot = idx < remainingCount}
              <span
                class="h-3.5 w-3.5 rounded-full transition-all duration-300 {isRemainingSlot
                  ? 'bg-amber-400 shadow-[0_0_8px_rgba(245,158,11,0.65)] scale-100'
                  : 'bg-stone-700/70 border border-stone-600 scale-90'}"
              ></span>
            {/each}
          </div>
        </div>
      {/if}
    </section>

    <!-- Optional Event Code Prompt -->
    {#if needsEventCode || showEventCodeEditor}
      <section
        class="rounded-2xl border border-amber-500/40 bg-stone-900/95 p-4 space-y-2.5 shadow-lg"
      >
        <div class="flex items-center justify-between">
          <label for="event-code-input" class="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-amber-300">
            <KeyRound class="w-3.5 h-3.5" />
            <span>Room Event Code Required</span>
          </label>
          {#if eventCode.trim()}
            <button
              type="button"
              onclick={() => (showEventCodeEditor = false)}
              class="text-xs text-stone-400 hover:text-stone-200"
            >
              Done
            </button>
          {/if}
        </div>
        <p class="text-xs text-stone-400">
          Enter the room code shown on the projector QR card (auto-filled when scanning the QR code).
        </p>
        <input
          id="event-code-input"
          type="text"
          value={eventCode}
          oninput={handleEventCodeInput}
          placeholder="e.g. GRAD2026"
          class="w-full rounded-xl bg-stone-950 border border-stone-700 px-3.5 py-2.5 text-sm text-[#faf6f0] placeholder:text-stone-600 focus:outline-none focus:border-amber-500"
        />
      </section>
    {/if}

    <!-- Success Upload Toast Banner -->
    {#if uploadSuccessToast.visible}
      <div
        role="status"
        class="rounded-2xl border border-emerald-500/40 bg-emerald-950/80 px-4 py-3.5 text-emerald-200 shadow-xl flex items-center justify-between gap-3"
      >
        <div class="flex items-center gap-2.5">
          <CheckCircle2 class="w-5 h-5 text-emerald-400 shrink-0" />
          <div class="text-xs sm:text-sm">
            <p class="font-bold text-emerald-100">Polaroid developed &amp; sent to the Big Screen!</p>
            <p class="text-emerald-300/90 truncate max-w-[250px]">{uploadSuccessToast.caption}</p>
          </div>
        </div>
        <button
          type="button"
          onclick={() => (uploadSuccessToast = { visible: false, caption: '' })}
          class="text-emerald-400 hover:text-emerald-200 p-1"
          aria-label="Dismiss notification"
        >
          <X class="w-4 h-4" />
        </button>
      </div>
    {/if}

    <!-- Initial Connection Error Banner -->
    {#if loadError}
      <div class="rounded-2xl border border-red-500/40 bg-red-950/60 p-4 text-sm text-red-200 flex items-center justify-between gap-3">
        <div class="flex items-center gap-2.5">
          <AlertCircle class="w-5 h-5 text-red-400 shrink-0" />
          <span>{loadError}</span>
        </div>
        <button
          type="button"
          onclick={loadInitialState}
          class="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-red-500/20 hover:bg-red-500/30 text-xs font-semibold text-red-200"
        >
          <RefreshCw class="w-3.5 h-3.5" />
          <span>Retry</span>
        </button>
      </div>
    {/if}

    <!-- Guided composer: name, then camera or upload, then caption (shown while the guest has photos left) -->
    {#if !config.uploadsOpen && !loadingInitial}
      <section class="rounded-2xl border border-stone-700 bg-stone-900/90 p-5 text-center" role="status">
        <h2 class="font-display text-lg font-bold">Uploads are closed</h2>
        <p class="mt-1 text-sm text-stone-300">The host has stopped new photos for this event. You can still send reactions to the wall.</p>
      </section>
    {:else if !isQuotaFull}
      <!-- Hidden pickers: gallery, and the phone's own camera app as a fallback for the in-page camera -->
      <input
        bind:this={fileInputEl}
        type="file"
        accept="image/*"
        onchange={handleFileSelected}
        class="hidden"
        tabindex="-1"
        aria-hidden="true"
      />
      <input
        bind:this={cameraInputEl}
        type="file"
        accept="image/*"
        capture="environment"
        onchange={handleFileSelected}
        class="hidden"
        tabindex="-1"
        aria-hidden="true"
      />

      <section aria-labelledby="step-heading" class="rounded-2xl border border-stone-800 bg-stone-900/90 p-4 sm:p-5 space-y-5 shadow-lg">
        <ol class="flex items-center gap-2 text-xs" aria-label="Progress">
          {#each STEPS as s, i (s.key)}
            {@const done = i < currentStepIndex}
            {@const current = i === currentStepIndex}
            <li class="flex items-center gap-1.5 {current ? 'text-[#faf6f0] font-semibold' : done ? 'text-stone-300' : 'text-stone-400'}" aria-current={current ? 'step' : undefined}>
              <span
                class="inline-flex h-6 w-6 items-center justify-center rounded-full text-[11px] font-bold {current
                  ? 'bg-amber-500 text-stone-950'
                  : done
                    ? 'bg-stone-700 text-stone-100'
                    : 'border border-stone-600 text-stone-400'}"
              >
                {#if done}<CheckCircle2 class="w-3.5 h-3.5" aria-hidden="true" />{:else}{i + 1}{/if}
              </span>
              <span>{s.label}</span>
            </li>
            {#if i < STEPS.length - 1}
              <li aria-hidden="true" class="h-px flex-1 bg-stone-700"></li>
            {/if}
          {/each}
        </ol>

        {#if step === 'name'}
          <form onsubmit={confirmNickname} class="space-y-4" novalidate>
            <div>
              <h2 id="step-heading" bind:this={stepHeadingEl} tabindex="-1" class="font-display text-xl font-bold text-[#faf6f0] focus:outline-none">
                What should we call you?
              </h2>
              <p class="mt-1 text-sm text-stone-300">Your name goes under every photo you send to the big screen.</p>
            </div>
            <div class="space-y-1.5">
              <label for="nickname-input" class="block text-sm font-semibold text-stone-200">Nickname</label>
              <input
                id="nickname-input"
                type="text"
                maxlength={MAX_NICKNAME_LENGTH}
                bind:value={nameDraft}
                autocomplete="nickname"
                enterkeyhint="next"
                placeholder="Your name"
                aria-invalid={nameError ? 'true' : undefined}
                aria-describedby={nameError ? 'nickname-error' : 'nickname-hint'}
                class="w-full min-h-11 rounded-xl bg-stone-950/90 border border-stone-700/90 px-3.5 py-2.5 text-base text-[#faf6f0] placeholder:text-stone-500 focus:outline-none focus:border-amber-400 focus:ring-2 focus:ring-amber-400/40"
              />
              {#if nameError}
                <p id="nickname-error" class="text-xs text-amber-200" role="alert">{nameError}</p>
              {:else}
                <p id="nickname-hint" class="text-xs text-stone-400">Saved on this phone, so you only type it once.</p>
              {/if}
            </div>
            <button type="submit" class="step-primary w-full">Continue</button>
          </form>
        {:else if step === 'source'}
          <div>
            <h2 id="step-heading" bind:this={stepHeadingEl} tabindex="-1" class="font-display text-xl font-bold text-[#faf6f0] focus:outline-none">
              Add a photo
            </h2>
            <p class="mt-1 text-sm text-stone-300">
              Posting as <span class="font-semibold text-[#faf6f0]">@{nickname}</span>
              <button type="button" onclick={editNickname} class="ml-1 inline-flex min-h-11 items-center gap-1 text-amber-300 underline underline-offset-4 hover:text-amber-200">
                <Pencil class="w-3 h-3" aria-hidden="true" />
                <span>Change</span>
              </button>
            </p>
          </div>

          <div class="grid grid-cols-2 gap-3">
            <button type="button" onclick={chooseCamera} class="source-card">
              <Camera class="w-8 h-8 text-amber-400" aria-hidden="true" />
              <span class="font-display text-base font-bold text-[#faf6f0]">Take a photo</span>
              <span class="text-xs text-stone-300">Use your camera now</span>
            </button>
            <button type="button" onclick={chooseUpload} class="source-card">
              <ImagePlus class="w-8 h-8 text-amber-400" aria-hidden="true" />
              <span class="font-display text-base font-bold text-[#faf6f0]">Upload a photo</span>
              <span class="text-xs text-stone-300">Pick one from your gallery</span>
            </button>
          </div>

          {#if uploadError}
            <div class="rounded-xl border border-red-500/40 bg-red-950/70 px-3.5 py-2.5 text-xs text-red-200 flex items-start gap-2" role="alert">
              <AlertCircle class="w-4 h-4 text-red-400 shrink-0 mt-0.5" aria-hidden="true" />
              <span>{uploadError}</span>
            </div>
          {/if}
        {:else if step === 'camera'}
          <div>
            <h2 id="step-heading" bind:this={stepHeadingEl} tabindex="-1" class="font-display text-xl font-bold text-[#faf6f0] focus:outline-none">
              Take a photo
            </h2>
            <p class="mt-1 text-sm text-stone-300">The square is exactly what lands on the wall.</p>
          </div>
          <CameraCapture onCapture={handleCameraCapture} onCancel={() => goTo('source')} onFallback={openPhoneCamera} />
        {:else}
          <form onsubmit={handleSubmit} class="space-y-4">
            <div>
              <h2 id="step-heading" bind:this={stepHeadingEl} tabindex="-1" class="font-display text-xl font-bold text-[#faf6f0] focus:outline-none">
                Add a caption
              </h2>
              <p class="mt-1 text-sm text-stone-300">Optional. It appears handwritten under your photo.</p>
            </div>

            <!-- Live Polaroid preview -->
            <div class="polaroid-paper rounded-md p-3.5 pb-5 text-stone-900">
              <div class="relative aspect-square w-full overflow-hidden rounded-[3px] bg-stone-900">
                {#if isCompressing || !compression}
                  <div class="flex h-full w-full flex-col items-center justify-center gap-3 p-6 text-center text-stone-200" role="status">
                    <RefreshCw class="w-8 h-8 text-amber-400 animate-spin" aria-hidden="true" />
                    <p class="font-display text-sm font-semibold">Developing your photo</p>
                    <p class="text-xs text-stone-400">Shrinking it so it sends fast on venue Wi-Fi</p>
                  </div>
                {:else}
                  <img
                    src={compression.previewUrl}
                    alt={caption.trim() ? `Your upload: ${caption.trim()}` : 'Your upload, ready to send'}
                    width={compression.width || 1600}
                    height={compression.height || 1600}
                    class="h-full w-full object-cover animate-develop"
                  />
                  {#if compression.wasCompressed && compression.compressedBytes < compression.originalBytes}
                    <span class="absolute bottom-2 left-2 rounded-md bg-stone-950/85 px-2 py-1 text-[11px] text-stone-200">
                      {formatBytes(compression.originalBytes)} → {formatBytes(compression.compressedBytes)}
                    </span>
                  {/if}
                {/if}
              </div>
              <div class="pt-3 px-1.5">
                <p class="font-handwriting text-2xl leading-snug text-stone-800 break-words min-h-8">
                  {#if caption.trim()}
                    “{caption.trim()}”
                  {:else}
                    <span class="text-stone-500">Your caption shows here</span>
                  {/if}
                </p>
                <p class="mt-2 border-t border-stone-300/70 pt-2 text-xs font-semibold text-stone-700 truncate">@{nickname}</p>
              </div>
            </div>

            <div class="space-y-1.5">
              <div class="flex items-center justify-between">
                <label for="caption-input" class="text-sm font-semibold text-stone-200">Caption</label>
                <span class="text-xs font-mono {captionLength >= MAX_CAPTION_LENGTH ? 'text-amber-300 font-semibold' : 'text-stone-400'}">
                  {captionLength}/{MAX_CAPTION_LENGTH}
                </span>
              </div>
              <input
                id="caption-input"
                type="text"
                maxlength={MAX_CAPTION_LENGTH}
                bind:value={caption}
                enterkeyhint="send"
                placeholder="Demo day win, late-night debugging, best cohort ever"
                class="w-full min-h-11 rounded-xl bg-stone-950/90 border border-stone-700/90 px-3.5 py-2.5 text-base text-[#faf6f0] placeholder:text-stone-500 focus:outline-none focus:border-amber-400 focus:ring-2 focus:ring-amber-400/40"
              />
            </div>

            {#if uploadError}
              <div class="rounded-xl border border-red-500/40 bg-red-950/70 px-3.5 py-2.5 text-xs text-red-200 flex items-start gap-2" role="alert">
                <AlertCircle class="w-4 h-4 text-red-400 shrink-0 mt-0.5" aria-hidden="true" />
                <span>{uploadError}</span>
              </div>
            {/if}

            <div class="grid grid-cols-[auto_1fr] gap-2">
              <button type="button" onclick={backToSource} disabled={isUploading} class="step-secondary">
                <ArrowLeft class="w-4 h-4" aria-hidden="true" />
                <span>Other photo</span>
              </button>
              <button type="submit" disabled={!canSubmit} class="step-primary">
                {#if isUploading}
                  <RefreshCw class="w-4 h-4 animate-spin" aria-hidden="true" />
                  <span>Sending</span>
                {:else}
                  <Send class="w-4 h-4" aria-hidden="true" />
                  <span>Send to wall</span>
                {/if}
              </button>
            </div>
            <p class="text-center text-xs text-stone-400">
              You can send {remainingCount} more {remainingCount === 1 ? 'photo' : 'photos'}.
            </p>
          </form>
        {/if}
      </section>
    {/if}

    <!-- Live Emoji Reaction Bar -->
    <section
      aria-label="Live Emoji Reactions"
      class="relative rounded-2xl border border-stone-800 bg-stone-900/90 p-4 shadow-lg overflow-visible"
    >
      <!-- Local Floating Emoji Burst Overlay -->
      <div class="pointer-events-none absolute inset-x-0 -top-4 flex justify-center overflow-visible z-20">
        {#each reactionBursts as burst (burst.id)}
          <span class="text-3xl animate-emoji-burst select-none">
            {burst.emoji}
          </span>
        {/each}
      </div>

      <div class="flex items-center justify-between mb-2.5">
        <div>
          <h2 class="font-display text-xs font-bold uppercase tracking-wider text-amber-300 flex items-center gap-1.5">
            <Sparkles class="w-3.5 h-3.5" />
            <span>Launch Live Reactions to the Wall</span>
          </h2>
          <p class="text-[11px] text-stone-400">
            Tap any emoji to float it across the projector screen in real time
          </p>
        </div>
      </div>

      <div class="grid grid-cols-4 sm:grid-cols-8 gap-2">
        {#each ALLOWED_REACTION_EMOJIS as emoji}
          <button
            type="button"
            onclick={() => handleReactionTap(emoji)}
            aria-label="Send {emoji} reaction"
            class="h-11 rounded-xl border border-stone-700/80 bg-stone-950/80 hover:bg-stone-800 hover:border-amber-500/50 active:scale-90 transition flex items-center justify-center text-xl shadow-sm cursor-pointer {lastTappedEmoji ===
            emoji
              ? 'ring-2 ring-amber-400 bg-amber-500/20 scale-95'
              : ''}"
          >
            <span>{emoji}</span>
          </button>
        {/each}
      </div>
    </section>

    <!-- "Your Shared Memories" Mini Polaroid Strip -->
    <section aria-label="Your Shared Memories" class="space-y-3 pt-1">
      <div class="flex items-center justify-between">
        <h2 class="font-display text-sm font-bold uppercase tracking-wider text-stone-300">
          Your Shared Memories ({me.photos?.length || 0}/{maxQuota})
        </h2>
        {#if (me.photos?.length || 0) > 0}
          <a href="/{room}/wall" class="text-xs text-amber-400 hover:text-amber-300 font-medium">
            View all on Wall →
          </a>
        {/if}
      </div>

      {#if loadingInitial}
        <div class="grid grid-cols-2 gap-3.5">
          {#each [1, 2] as placeholder}
            <div class="rounded-md bg-stone-900/70 border border-stone-800 p-2.5 pb-4 animate-pulse">
              <div class="aspect-square w-full bg-stone-800 rounded-sm"></div>
              <div class="h-4 w-3/4 bg-stone-800 rounded mt-3"></div>
            </div>
          {/each}
        </div>
      {:else if !me.photos || me.photos.length === 0}
        <div class="rounded-2xl border border-dashed border-stone-800 bg-stone-900/40 p-6 text-center">
          <p class="text-xs text-stone-400">
            You haven’t snapped any graduation Polaroids yet. Your {maxQuota} personal keepsakes will appear right here!
          </p>
        </div>
      {:else}
        <div class="grid grid-cols-2 gap-4 pt-1">
          {#each me.photos as photo, idx (photo.id)}
            <button
              type="button"
              onclick={() => (activeModalPhoto = photo)}
              style="transform: rotate({deterministicTilt(photo.id, idx)}deg);"
              class="polaroid-paper rounded-sm p-2.5 pb-4 text-left text-stone-900 transition hover:scale-[1.02] hover:rotate-0 focus:outline-none focus:ring-2 focus:ring-amber-400 cursor-pointer"
            >
              <div class="aspect-square w-full overflow-hidden rounded-[2px] bg-stone-200">
                <img
                  src={photo.imageUrl}
                  alt={photo.caption || `Memory by ${photo.nickname}`}
                  class="w-full h-full object-cover"
                  loading="lazy"
                />
              </div>
              <div class="mt-2.5 px-0.5">
                {#if photo.caption}
                  <p class="font-handwriting text-lg leading-tight text-stone-800 line-clamp-2">
                    “{photo.caption}”
                  </p>
                {:else}
                  <p class="font-handwriting text-base leading-tight text-stone-400 italic">
                    Grad memory #{me.photos.length - idx}
                  </p>
                {/if}
                <div class="mt-1.5 flex items-center justify-between text-[10px] text-stone-500">
                  <span class="font-semibold truncate max-w-[90px]">@{photo.nickname}</span>
                  <span class="inline-flex items-center gap-0.5">
                    <Clock class="w-2.5 h-2.5" />
                    {formatRelativeTime(photo.createdAt)}
                  </span>
                </div>
              </div>
            </button>
          {/each}
        </div>
      {/if}
    </section>
  </main>

  <!-- Lightbox Modal for Viewing Own Uploaded Polaroid -->
  {#if activeModalPhoto}
    <div
      role="dialog"
      aria-modal="true"
      class="fixed inset-0 z-50 bg-black/80 backdrop-blur-sm flex items-center justify-center p-5"
    >
      <div class="relative w-full max-w-sm">
        <button
          type="button"
          onclick={() => (activeModalPhoto = null)}
          class="absolute -top-11 right-0 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-stone-800 text-stone-200 text-xs font-semibold hover:bg-stone-700"
        >
          <X class="w-4 h-4" />
          <span>Close</span>
        </button>

        <div class="polaroid-paper rounded-md p-4 pb-6 text-stone-900 shadow-2xl">
          <div class="aspect-square w-full overflow-hidden rounded-[2px] bg-stone-200">
            <img
              src={activeModalPhoto.imageUrl}
              alt={activeModalPhoto.caption || activeModalPhoto.nickname}
              class="w-full h-full object-cover"
            />
          </div>
          <div class="mt-4 px-1">
            {#if activeModalPhoto.caption}
              <p class="font-handwriting text-2xl leading-snug text-stone-800">
                “{activeModalPhoto.caption}”
              </p>
            {/if}
            <div class="mt-3 pt-2 border-t border-stone-300/80 flex items-center justify-between text-xs text-stone-500">
              <span class="font-bold text-stone-700">@{activeModalPhoto.nickname}</span>
              <span>{formatRelativeTime(activeModalPhoto.createdAt)}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  {/if}
</div>
{/if}

<style>
  /* Two big choices on the photo step: large targets for one-handed use at a crowded venue. */
  .source-card {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.5rem;
    min-height: 8.5rem;
    padding: 1rem;
    border-radius: 0.875rem;
    border: 1px solid #57534e;
    background: #0c0a09;
    text-align: left;
    transition:
      border-color 120ms ease,
      background-color 120ms ease,
      transform 80ms ease;
  }

  .source-card:hover {
    border-color: #f59e0b;
    background: #1c1917;
  }

  .source-card:active {
    transform: scale(0.98);
  }

  .step-primary,
  .step-secondary {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
    min-height: 48px;
    padding: 0 1.1rem;
    border-radius: 0.75rem;
    font-weight: 700;
    font-size: 0.95rem;
    transition:
      background-color 120ms ease,
      transform 80ms ease;
  }

  .step-primary {
    background: #f59e0b;
    color: #1c1917;
  }

  .step-primary:hover:not(:disabled) {
    background: #fbbf24;
  }

  .step-secondary {
    border: 1px solid #57534e;
    background: #1c1917;
    color: #faf6f0;
  }

  .step-secondary:hover:not(:disabled) {
    background: #292524;
  }

  .step-primary:active:not(:disabled),
  .step-secondary:active:not(:disabled) {
    transform: translateY(1px);
  }

  .step-primary:disabled,
  .step-secondary:disabled {
    background: #292524;
    color: #a8a29e;
    cursor: not-allowed;
  }

  .source-card:focus-visible,
  .step-primary:focus-visible,
  .step-secondary:focus-visible {
    outline: 3px solid #fbbf24;
    outline-offset: 2px;
  }

  @media (prefers-reduced-motion: reduce) {
    .source-card,
    .step-primary,
    .step-secondary {
      transition: none;
    }
  }
</style>
