export interface Recording { stop(): Promise<Blob>; cancel(): void }

export async function startRecording(onStopped?: () => void): Promise<Recording> {
  if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") throw new Error("当前浏览器不支持录音，请使用 HTTPS 或 localhost 打开");
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
  try {
    const mimeType = ["audio/webm;codecs=opus", "audio/mp4", "audio/ogg;codecs=opus"].find((type) => MediaRecorder.isTypeSupported(type));
    const recorder = new MediaRecorder(stream, mimeType ? { mimeType } : undefined);
    const chunks: Blob[] = [];
    let bytes = 0;
    let oversized = false;
    let stopped = false;
    const release = () => stream.getTracks().forEach((track) => track.stop());
    const completed = new Promise<Blob>((resolve, reject) => {
      recorder.ondataavailable = ({ data }) => {
        bytes += data.size;
        if (bytes > 10 * 1024 * 1024) { oversized = true; if (recorder.state !== "inactive") recorder.stop(); }
        else if (data.size) chunks.push(data);
      };
      recorder.onerror = () => { release(); reject(new Error("录音失败，请重试")); };
      recorder.onstop = () => { release(); oversized ? reject(new Error("录音过长，请控制在 2 分钟以内")) : resolve(new Blob(chunks, { type: recorder.mimeType || "audio/webm" })); onStopped?.(); };
    });
    // Avoid unhandled rejections when a cancelled recording finishes in teardown.
    void completed.catch(() => undefined);
    recorder.start(1000);
    const timer = window.setTimeout(() => { if (recorder.state !== "inactive") recorder.stop(); }, 120000);
    return {
      stop: () => { clearTimeout(timer); if (!stopped && recorder.state !== "inactive") recorder.stop(); stopped = true; return completed; },
      cancel: () => { clearTimeout(timer); if (recorder.state !== "inactive") recorder.stop(); release(); stopped = true; },
    };
  } catch (error) { stream.getTracks().forEach((track) => track.stop()); throw error; }
}
