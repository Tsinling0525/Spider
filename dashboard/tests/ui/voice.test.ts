import { afterEach, expect, it, vi } from "vitest";
import { startRecording } from "../../src/lib/voice";

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

function microphoneFixture() {
  const release = vi.fn();
  const stream = { getTracks: () => [{ stop: release }] };
  class Recorder {
    static isTypeSupported(type: string) { return type === "audio/mp4"; }
    state = "inactive";
    mimeType = "audio/mp4";
    onstop?: () => void;
    onerror?: () => void;
    ondataavailable?: (event: { data: Blob }) => void;
    start() { this.state = "recording"; }
    stop() { this.state = "inactive"; this.ondataavailable?.({ data: new Blob(["audio"]) }); this.onstop?.(); }
  }
  vi.stubGlobal("MediaRecorder", Recorder);
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn().mockResolvedValue(stream) } });
  return { release, Recorder };
}

it("releases microphone tracks when recording ends or is cancelled", async () => {
  const { release } = microphoneFixture();
  const session = await startRecording();
  const file = await session.stop();
  expect(file.type).toBe("audio/mp4");
  expect(release).toHaveBeenCalled();
  release.mockClear();
  const cancelled = await startRecording(); cancelled.cancel();
  expect(release).toHaveBeenCalled();
});

it("automatically ends after two minutes and notifies the composer", async () => {
  vi.useFakeTimers(); const { release } = microphoneFixture(); const stopped = vi.fn();
  await startRecording(stopped); await vi.advanceTimersByTimeAsync(120000);
  expect(release).toHaveBeenCalledOnce();
  expect(stopped).toHaveBeenCalledOnce();
});

it("releases granted microphone access if MediaRecorder construction fails", async () => {
  const { release } = microphoneFixture();
  class BrokenRecorder { static isTypeSupported() { return true; }; constructor() { throw new Error("unsupported recorder"); } }
  vi.stubGlobal("MediaRecorder", BrokenRecorder);
  await expect(startRecording()).rejects.toThrow("unsupported recorder");
  expect(release).toHaveBeenCalledOnce();
});
