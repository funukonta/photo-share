<script lang="ts">
  import type { Photo } from '../lib/api';
  import { relativeTime, tiltFor } from '../lib/wall';

  interface Props {
    photo: Photo;
    now: number;
    /** Plays the slow "developing" chemistry fade on the photo. */
    developing?: boolean;
    /** Overrides the deterministic tilt (the spotlight card sits closer to level). */
    tilt?: number;
    eager?: boolean;
  }

  let { photo, now, developing = false, tilt, eager = false }: Props = $props();

  let angle = $derived(tilt ?? tiltFor(photo.id));
  let when = $derived(relativeTime(photo.createdAt, now));
</script>

<div class="frame">
<figure class="pin polaroid-paper" style="--tilt: {angle}deg">
  <span class="tape" aria-hidden="true"></span>
  <div class="photo">
    <img
      src={photo.imageUrl}
      alt={photo.caption ? `${photo.caption}, shared by ${photo.nickname}` : `Photo shared by ${photo.nickname}`}
      width="600"
      height="600"
      loading={eager ? 'eager' : 'lazy'}
      decoding="async"
      class:develop={developing}
    />
  </div>
  <figcaption>
    <p class="caption font-handwriting">{photo.caption}</p>
    <p class="meta">
      <span class="nick">@{photo.nickname}</span>
      {#if when}
        <time datetime={photo.createdAt}>{when}</time>
      {/if}
    </p>
  </figcaption>
</figure>
</div>

<style>
  /* All sizes use container-query units so the same card works at 150px on a full wall and 560px in the spotlight.
     The container is the outer frame: an element's own cqw resolves against its ancestor, never itself. */
  .frame {
    container-type: inline-size;
    width: 100%;
  }

  .pin {
    position: relative;
    width: 100%;
    margin: 0;
    padding: 5cqw 5cqw 0;
    border-radius: 0.6cqw;
    color: #1c1917;
    transform: rotate(var(--tilt));
  }

  .tape {
    position: absolute;
    top: -3.5cqw;
    left: 50%;
    width: 34cqw;
    height: 8.5cqw;
    transform: translateX(-50%) rotate(calc(var(--tilt) * -1.4));
    background: rgba(240, 228, 196, 0.62);
    border-left: 1px dashed rgba(120, 100, 60, 0.18);
    border-right: 1px dashed rgba(120, 100, 60, 0.18);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.18);
  }

  .photo {
    aspect-ratio: 1 / 1;
    overflow: hidden;
    background: #2a2622;
    border-radius: 0.4cqw;
  }

  img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
    /* Faces in portrait phone shots usually sit in the upper third. */
    object-position: 50% 35%;
  }

  .develop {
    animation: developSlow 3.2s cubic-bezier(0.22, 1, 0.36, 1) both;
  }

  figcaption {
    height: 28cqw;
    padding: 2.5cqw 1cqw 0;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    overflow: hidden;
  }

  .caption {
    margin: 0;
    font-size: 7.6cqw;
    line-height: 1.02;
    color: #292524;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    overflow-wrap: anywhere;
  }

  .meta {
    margin: 0 0 3.2cqw;
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 3cqw;
    font-size: 4.6cqw;
    line-height: 1.2;
  }

  .nick {
    font-weight: 700;
    color: #44403c;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  time {
    flex-shrink: 0;
    color: #57534e;
  }

  @keyframes developSlow {
    0% {
      filter: brightness(0.25) contrast(0.4) sepia(0.9) saturate(0.1) blur(6px);
    }
    35% {
      filter: brightness(1.5) contrast(0.45) sepia(0.6) saturate(0.25) blur(3px);
    }
    75% {
      filter: brightness(1.08) contrast(0.9) sepia(0.15) saturate(0.85) blur(0.4px);
    }
    100% {
      filter: none;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .develop {
      animation: none;
    }
  }
</style>
