# Step 6 — Guided Upload Flow with In-Page Camera (`/`)

- **Status:** Done and tested locally. **Not deployed to the Sprite yet** (deploy only after local sign-off).
- **Related Plan:** [`doc/initial-plan.md`](./initial-plan.md)

## 1. Goal
The old flow had the guest pick a photo first and type their nickname afterwards. The new flow:

> open the page → **nickname** → **Take a photo** (camera) or **Upload a photo** (gallery) → **caption** → send

## 2. Change Log

### `frontend/src/lib/camera.ts` (new)
- `inPageCameraSupported()`: true when `getUserMedia` is available in a secure context (HTTPS or localhost).

### `frontend/src/components/CameraCapture.svelte` (new)
- In-page camera using `getUserMedia`.
  - Starts on the front camera (selfies with friends).
  - A switch button appears only when the device has more than one camera.
  - The square viewfinder is exactly the area that gets saved: centre square crop, up to 1600×1600 JPEG.
  - Front-camera shots are saved mirrored, matching the preview.
- Shutter → short flash → review screen with **Retake** / **Use this photo**. The camera stops while reviewing, so the recording light goes off.
- **Escape** or **Cancel** stops the camera and goes back.
- The camera is released when the tab is hidden and restarted when the guest comes back.
- **Errors** (blocked permission, no camera, camera busy) show a plain message plus an **Open phone camera** button. That button opens the phone's own camera app through `<input type="file" capture>`, then continues the same flow.

### `frontend/src/routes/Upload.svelte`
- **Composer is now a guided flow** with a three-step progress bar: Name, Photo, Caption.
  - **Name:** asked first, with a validation message. It is saved on the phone, so returning guests start at the photo step. "Change" reopens it, prefilled.
  - **Photo:** two large choices.
    - **Take a photo** opens the in-page camera. If the browser can't run it (plain-HTTP address, some in-app browsers), it opens the phone camera app directly.
    - **Upload a photo** opens the gallery picker as before.
  - **Caption:** live Polaroid preview, the existing compression, an optional caption, **Other photo** (back), and **Send to wall**. After sending, the guest returns to the photo step with the success toast.
- Focus moves to each step's heading on every step change (keyboard and screen readers).
- Inputs are at least 44px tall with 16px text, so iOS does not zoom when an input is focused.
- Removed the old single-card picker, the per-keystroke nickname field, and the "Swap Photo" overlay.
- Unchanged: quota pill, event code prompt, reactions, "Your shared memories".

## 3. Verification (local only: Go backend with `STORAGE_BACKEND=fs`, headless Chromium with a fake camera, 390×844)
- `npm run check`: 0 errors, 0 warnings. `npm run build`: succeeded.
- **25/25 browser checks passed:**
  - **Name step:**
    - first visit opens on it
    - an empty name shows an error
    - Enter continues to the photo step, with focus on the step heading
  - **Camera path:**
    - the camera starts and the shutter opens the review screen
    - the camera stops while reviewing, and Retake restarts it
    - "Use this photo" leads to the caption step, and sending returns to the photo step
    - the uploaded photo has the nickname and caption and is square (1600×1600)
  - **Upload path:**
    - the gallery picker opens without a `capture` attribute
    - the chosen photo leads to the caption step
    - "Other photo" goes back to the photo step
  - **Name changes and returning guests:**
    - "Change" reopens the name prefilled, and the new name is used for the next upload
    - after a reload, a returning guest starts at the photo step
  - **Cancelling and the photo limit:**
    - Escape cancels the camera and releases it
    - after 5 photos the composer is replaced by the "shared all" card
  - **Camera fallbacks:**
    - a blocked camera offers "Open phone camera" (`capture="environment"`), which continues to the caption step
    - with no `getUserMedia` at all, "Take a photo" opens the phone camera directly
  - **Layout:** no horizontal overflow on a phone screen.
  - **Errors:** no page errors.
- Still to check on real phones (iOS Safari, Android Chrome) over HTTPS before the event: permission prompt wording, front/back switching, and how fast the camera starts.
