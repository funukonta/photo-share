<script lang="ts">
  import type { ReactionPayload } from '../lib/api';

  interface FloatingEmoji {
    key: number;
    emoji: string;
    nickname: string;
    left: number; // % of viewport width
    drift: number; // px of sideways sway
    duration: number; // ms
    size: number; // rem
  }

  /** Caps DOM nodes during a reaction storm; the oldest emojis are dropped first. */
  const MAX_ON_SCREEN = 48;

  let items = $state<FloatingEmoji[]>([]);
  let seq = 0;

  export function push(reaction: Pick<ReactionPayload, 'emoji' | 'nickname'>) {
    const item: FloatingEmoji = {
      key: ++seq,
      emoji: reaction.emoji,
      nickname: reaction.nickname?.trim() || '',
      left: 4 + Math.random() * 88,
      drift: (Math.random() * 2 - 1) * 70,
      duration: 4200 + Math.random() * 1800,
      size: 2.4 + Math.random() * 1.6,
    };
    items = [...items.slice(-(MAX_ON_SCREEN - 1)), item];
  }

  function remove(key: number) {
    items = items.filter((i) => i.key !== key);
  }
</script>

<div class="layer" aria-hidden="true">
  {#each items as item (item.key)}
    <div
      class="float"
      style="left: {item.left}%; --drift: {item.drift}px; --dur: {item.duration}ms; --size: {item.size}rem"
      onanimationend={() => remove(item.key)}
    >
      <span class="glyph">{item.emoji}</span>
      {#if item.nickname}
        <span class="who">{item.nickname}</span>
      {/if}
    </div>
  {/each}
</div>

<style>
  .layer {
    position: fixed;
    inset: 0;
    pointer-events: none;
    overflow: hidden;
    z-index: 60;
  }

  .float {
    position: absolute;
    bottom: -5rem;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.15rem;
    animation: rise var(--dur) cubic-bezier(0.33, 0.1, 0.4, 1) forwards;
    will-change: transform, opacity;
  }

  .glyph {
    font-size: var(--size);
    line-height: 1;
    filter: drop-shadow(0 4px 10px rgba(0, 0, 0, 0.45));
  }

  .who {
    max-width: 9rem;
    padding: 0.1rem 0.45rem;
    border-radius: 0.35rem;
    background: rgba(20, 18, 16, 0.78);
    color: #faf6f0;
    font-size: 0.8rem;
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  @keyframes rise {
    0% {
      transform: translate(0, 0) scale(0.6);
      opacity: 0;
    }
    10% {
      transform: translate(calc(var(--drift) * 0.2), -10vh) scale(1.05);
      opacity: 1;
    }
    45% {
      transform: translate(var(--drift), -48vh) scale(1);
    }
    75% {
      transform: translate(calc(var(--drift) * -0.4), -80vh) scale(0.95);
      opacity: 0.9;
    }
    100% {
      transform: translate(calc(var(--drift) * 0.3), -112vh) scale(0.9);
      opacity: 0;
    }
  }

  /* Reduced motion: no travel, the emoji appears near the bottom edge and fades out in place. */
  @keyframes fadeInPlace {
    0% {
      transform: translateY(-12vh);
      opacity: 0;
    }
    15%,
    70% {
      transform: translateY(-12vh);
      opacity: 1;
    }
    100% {
      transform: translateY(-12vh);
      opacity: 0;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .float {
      animation-name: fadeInPlace;
      animation-duration: 2.5s;
    }
  }
</style>
