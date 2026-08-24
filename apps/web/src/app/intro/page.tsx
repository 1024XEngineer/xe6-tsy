"use client";

import { ArrowRight, ArrowUpRight, Check } from "@phosphor-icons/react";

import { BackToTop, Reveal, SiteFooter, SiteNav } from "./site-shell";
import { siteHref } from "./site-paths";
import styles from "./intro.module.css";

export default function IntroPage() {
  return (
    <main className={styles.site}>
      <SiteNav />

      <section className={styles.hero} id="top">
        <div className={styles.heroCopy}>
          <p className={styles.kicker}><span className={styles.liveDot} />实时语音系统 · 技术预览</p>
          <h1><span>面对面</span><span>实时同传，</span><em>沟通自然发生。</em></h1>
          <p className={styles.heroLead}>
            Lingow 将语音识别、翻译与语音播报连接在同一条实时链路中，让不同语言的人保持对话，而不是轮流等待。
          </p>
          <div className={styles.heroActions}>
            <a className={styles.primaryButton} href="#contact">预约体验 <ArrowUpRight size={18} weight="bold" /></a>
            <a className={styles.textButton} href={siteHref("/intro/developer")}>查看开发者入口 <ArrowRight size={18} /></a>
          </div>
          <div className={styles.heroMeta}>
            <span>Web 入口</span><span>·</span><span>双向对话</span><span>·</span><span>中文 ↔ English</span>
          </div>
        </div>

        <div className={styles.heroVisual} aria-label="实时同传演示占位">
          <div className={styles.visualHeader}>
            <span className={styles.status}><span className={styles.liveDot} />LIVE PREVIEW</span>
            <span className={styles.visualCode}>SESSION / 001</span>
          </div>
          <div className={styles.placeholderFrame}>
            <div className={styles.placeholderLabel}>真实产品截图 / 录屏占位</div>
            <div className={styles.speechCard}>
              <div className={styles.speechTop}><span>中文</span><span>00:08</span></div>
              <p>我们可以从今天的议程开始吗？</p>
              <div className={styles.wave}><i /><i /><i /><i /><i /><i /><i /><i /><i /><i /><i /></div>
            </div>
            <div className={`${styles.speechCard} ${styles.translatedCard}`}>
              <div className={styles.speechTop}><span>English · 翻译完成</span><Check size={14} weight="bold" /></div>
              <p>Shall we start with today&apos;s agenda?</p>
              <span className={styles.playback}>正在播报 <span className={styles.playbackLine} /></span>
            </div>
          </div>
          <div className={styles.visualFooter}><span>LISTENING</span><span>TRANSLATING</span><span>SPEAKING</span></div>
        </div>
      </section>

      <Reveal><section className={styles.introBand}>
        <div className={styles.sectionEyebrow}>WHY LINGOW</div>
        <div className={styles.introStatement}>
          <p>语言不应该成为面对面交流的界面。</p>
          <p className={styles.mutedStatement}>Lingow 让声音直接抵达另一种语言。</p>
        </div>
      </section></Reveal>

      <Reveal><section className={styles.contactSection} id="contact">
        <p className={styles.kicker}>READY WHEN YOU ARE</p>
        <h2>为下一次跨语言交流，<br /><em>准备一个自然的开始。</em></h2>
        <a className={styles.primaryButton} href="mailto:hello@lingow.example">预约体验 <ArrowUpRight size={18} weight="bold" /></a>
        <p className={styles.contactNote}>体验入口、录屏和正式联系邮箱将在素材确认后替换。</p>
      </section></Reveal>

      <SiteFooter />
      <BackToTop />
    </main>
  );
}
