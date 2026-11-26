<script lang="ts">
  import { onMount } from 'svelte';
  import Upload from './routes/Upload.svelte';
  import Wall from './routes/Wall.svelte';
  import Admin from './routes/Admin.svelte';
  import AdminHome from './routes/AdminHome.svelte';
  import Join from './routes/Join.svelte';
  import HostGate from './components/HostGate.svelte';
  import { normalizeRoomCode } from './lib/api';

  type Route =
    | { page: 'join'; notFound?: string }
    | { page: 'admin-home' }
    | { page: 'admin-event'; room: string }
    | { page: 'upload'; room: string }
    | { page: 'wall'; room: string };

  // URLs:
  //   /                 join a room (type the code)
  //   /admin            host: all events, create new
  //   /admin/<room>     host: moderate one event
  //   /<room>           guest upload page
  //   /<room>/wall      projector wall
  function parse(pathname: string): Route {
    const parts = pathname.split('/').filter(Boolean);
    if (parts.length === 0) return { page: 'join' };
    if (parts[0] === 'admin') {
      if (parts.length === 1) return { page: 'admin-home' };
      const room = normalizeRoomCode(parts[1]);
      return room && parts.length === 2 ? { page: 'admin-event', room } : { page: 'admin-home' };
    }
    const room = normalizeRoomCode(parts[0]);
    if (!room) return { page: 'join', notFound: parts[0] };
    if (parts.length === 1) return { page: 'upload', room };
    if (parts.length === 2 && parts[1] === 'wall') return { page: 'wall', room };
    return { page: 'join', notFound: parts.join('/') };
  }

  let route = $state<Route>(parse(typeof window !== 'undefined' ? window.location.pathname : '/'));
  let adminRoom = $derived(route.page === 'admin-event' ? route.room : '');

  onMount(() => {
    const update = () => (route = parse(window.location.pathname));
    window.addEventListener('popstate', update);
    return () => window.removeEventListener('popstate', update);
  });
</script>

{#if route.page === 'wall'}
  {#key route.room}<Wall room={route.room} />{/key}
{:else if route.page === 'upload'}
  {#key route.room}<Upload room={route.room} />{/key}
{:else if route.page === 'admin-home'}
  <HostGate>
    {#snippet children()}<AdminHome />{/snippet}
  </HostGate>
{:else if route.page === 'admin-event'}
  <HostGate>
    {#snippet children()}{#key adminRoom}<Admin room={adminRoom} />{/key}{/snippet}
  </HostGate>
{:else}
  <Join notFound={route.notFound} />
{/if}
