import styles from "../voice.module.css";

export function VideoOrb() {
  return (
    <span className={styles.videoOrb} aria-hidden="true">
      <video
        autoPlay
        className={styles.videoOrbMedia}
        controls={false}
        controlsList="nodownload nofullscreen noremoteplayback"
        data-testid="idle-voice-video"
        disablePictureInPicture
        disableRemotePlayback
        loop
        muted
        playsInline
        preload="metadata"
        src="/media/loop.mp4"
        tabIndex={-1}
      />
    </span>
  );
}
