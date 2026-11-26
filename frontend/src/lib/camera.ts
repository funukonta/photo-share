/**
 * True when the in-page camera (getUserMedia) can run. It needs HTTPS (or localhost) and a browser that
 * exposes mediaDevices; some in-app browsers (Instagram, LINE) and plain-HTTP LAN addresses do not.
 * When false, open the phone's own camera app through <input type="file" capture> instead.
 */
export function inPageCameraSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    window.isSecureContext &&
    typeof navigator !== 'undefined' &&
    !!navigator.mediaDevices?.getUserMedia
  );
}
