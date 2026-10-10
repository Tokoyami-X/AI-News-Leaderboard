(() => {
  'use strict';
  let audioContext;
  let lastPlayedAt = 0;

  function playClick() {
    const nowMs = performance.now();
    if (nowMs - lastPlayedAt < 45) return;
    lastPlayedAt = nowMs;

    try {
      const AudioContextClass = window.AudioContext || window.webkitAudioContext;
      if (!AudioContextClass) return;
      audioContext ||= new AudioContextClass();
      if (audioContext.state === 'suspended') audioContext.resume().catch(() => {});

      const now = audioContext.currentTime;
      const oscillator = audioContext.createOscillator();
      const gain = audioContext.createGain();
      oscillator.type = 'sine';
      oscillator.frequency.setValueAtTime(920, now);
      oscillator.frequency.exponentialRampToValueAtTime(620, now + 0.025);
      gain.gain.setValueAtTime(0.0001, now);
      gain.gain.exponentialRampToValueAtTime(0.045, now + 0.004);
      gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.035);
      oscillator.connect(gain);
      gain.connect(audioContext.destination);
      oscillator.start(now);
      oscillator.stop(now + 0.04);
      oscillator.onended = () => {
        oscillator.disconnect();
        gain.disconnect();
      };
    } catch (_) {
      // Audio is an enhancement; unsupported or blocked audio must not affect the page.
    }
  }

  document.addEventListener('pointerdown', event => {
    if (event.button !== 0) return;
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;
    const control = target.closest('button, a, select, [role="button"], .source-tab, .category-tab');
    if (!control || control.matches(':disabled, [aria-disabled="true"]')) return;
    playClick();
  }, { capture: true, passive: true });
})();
