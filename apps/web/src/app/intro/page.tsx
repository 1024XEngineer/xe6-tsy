"use client";

import {
  ArrowRight,
  ArrowUpRight,
  ChatCircleDots,
  Check,
  Command,
  Microphone,
  SpeakerHigh,
  Translate,
  Waveform,
} from "@phosphor-icons/react";
import Image from "next/image";
import { useState } from "react";

import { BackToTop, Reveal, SiteFooter, SiteNav } from "./site-shell";
import { siteHref, webExperienceHref } from "./site-paths";
import { LocaleProvider, Localized } from "./locale";
import styles from "./intro.module.css";

type Mode = "interpretation" | "assistant";

const modeContent = {
  interpretation: {
    eyebrow: "MODE / 01",
    title: <>面对面同传，<span>保持对话节奏。</span></>,
    copy: "为一组双语配置开启临时会话。系统自动识别发言，完成翻译，在句末播报，并允许另一方随时抢话。",
    items: ["双语配置与自动语言识别", "流式 ASR、翻译与句末播报", "抢话打断，双方可以自然接话"],
    path: [
      { label: "输入", value: "两种语言的现场语音", icon: Microphone },
      { label: "处理", value: "识别、翻译、固定当前 Turn", icon: Translate },
      { label: "输出", value: "目标语言播报与可选字幕", icon: SpeakerHigh },
    ],
  },
  assistant: {
    eyebrow: "MODE / 02",
    title: <>AI 语音助手，<span>用声音完成下一步。</span></>,
    copy: "唤醒 Lingow 后，用自然语言提问、执行命令或切换模式。助手与同传共用同一条 WebRTC 会话。",
    items: ["本地唤醒词“小灵小灵”", "自然语言问答与语义命令", "不重建连接即可切换工作方式"],
    path: [
      { label: "输入", value: "唤醒词与自然语言命令", icon: Command },
      { label: "处理", value: "命令解释与助手响应", icon: ChatCircleDots },
      { label: "输出", value: "语音回复、字幕或模式切换", icon: SpeakerHigh },
    ],
  },
} as const;

const pipeline = [
  { label: "麦克风", detail: "采集现场语音", icon: Microphone },
  { label: "VAD", detail: "找到一句话的边界", icon: Waveform },
  { label: "ASR", detail: "把声音变成文本", icon: ChatCircleDots },
  { label: "翻译", detail: "转换到另一种语言", icon: Translate },
  { label: "输出", detail: "TTS / 字幕 / 企业微信", icon: SpeakerHigh },
] as const;

export default function IntroPage() {
  const [mode, setMode] = useState<Mode>("interpretation");
  const selectedMode = modeContent[mode];

  return (
    <LocaleProvider>
      <main className={styles.site}>
      <SiteNav />
      <Localized>

      <section className={styles.hero} id="top">
        <div className={styles.heroInner}>
          <div className={styles.heroCopy}>
          <p className={styles.kicker}><span className={styles.liveDot} />实时语音系统 · Web 可运行</p>
          <h1><span>让两种语言，</span><span>在同一场对话里</span><em>自然来回。</em></h1>
          <p className={styles.heroLead}>Lingow 把语音识别、翻译和播报连接在同一条实时会话中，也支持通过声音完成命令和模式切换。</p>
          <div className={styles.heroActions}>
            <a className={styles.primaryButton} href={webExperienceHref}>立即打开 Web 体验 <ArrowUpRight size={18} weight="bold" /></a>
            <a className={styles.textButton} href={siteHref("/intro/docs")}>查看文档 <ArrowRight size={18} /></a>
          </div>
          </div>

          <div className={styles.heroVisual} aria-label="双语实时会话示意">
          <div className={styles.visualHeader}><span className={styles.status}><span className={styles.liveDot} />LIVE SESSION</span><span className={styles.visualCode}>WEBRTC / P0</span></div>
          <div className={styles.placeholderFrame}>
            <Image
              alt="Lingow 同声传译会话正在监听的真实界面"
              className={styles.realScreenshot}
              height={1356}
              src={siteHref("/media/voice-interpretation-listening.png")}
              width={2864}
            />
          </div>
          <div className={styles.visualFooter}><span>LISTENING</span><span>TRANSLATING</span><span>SPEAKING</span></div>
          </div>
        </div>
      </section>

      <Reveal><section className={styles.factSection} aria-labelledby="facts-title"><div className={styles.factInner}><div className={styles.factIntro}><p className={styles.sectionEyebrow}>CURRENTLY TRUE</p><h2 id="facts-title">先看事实，<span>再开始体验。</span></h2></div><div className={styles.factGrid}><div className={styles.factItem}><span className={styles.factStatus} /><strong>Web 当前可运行</strong><p>浏览器是现在主要的联调与体验入口。</p></div><div className={styles.factItem}><span className={styles.factStatus} /><strong>WebRTC 实时音频链路</strong><p>音频媒体与控制事件在同一条实时会话中完成。</p></div><div className={styles.factItem}><span className={styles.factStatus} /><strong>P0 不保存原始音频</strong><p>长期保存文本 Final Turn 和用量事实。</p></div></div></div></section></Reveal>

      <Reveal><section className={`${styles.productSection} ${styles.homeModes}`} id="modes" aria-labelledby="modes-title"><div className={styles.homeModesInner}><div className={styles.sectionHeading}><div><p className={styles.sectionEyebrow}>TWO WAYS TO WORK</p><h2 id="modes-title">同一条会话，<br /><span>两种工作方式。</span></h2></div><p>按现场需要选择入口。模式只改变业务处理方式，不重建已经建立的实时连接。</p></div><div className={styles.modeSwitch} role="tablist" aria-label="选择工作方式"><button className={mode === "interpretation" ? styles.modeTabActive : styles.modeTab} type="button" role="tab" aria-selected={mode === "interpretation"} onClick={() => setMode("interpretation")}>面对面同传</button><button className={mode === "assistant" ? styles.modeTabActive : styles.modeTab} type="button" role="tab" aria-selected={mode === "assistant"} onClick={() => setMode("assistant")}>AI 语音助手</button></div><div className={styles.modePanel} role="tabpanel"><div className={styles.modePanelCopy}><p className={styles.accentLabel}>{selectedMode.eyebrow}</p><h3>{selectedMode.title}</h3><p>{selectedMode.copy}</p><ul>{selectedMode.items.map((item) => <li key={item}><Check size={16} weight="bold" />{item}</li>)}</ul></div><div className={styles.modePanelVisual}><p className={styles.pathCaption}>INPUT <span /> PROCESS <span /> OUTPUT</p><div className={styles.modePath}>{selectedMode.path.map(({ label, value, icon: Icon }, index) => <div className={styles.modePathStep} key={label}><div className={styles.modePathIcon}><Icon size={21} weight="regular" /></div><div><span>{label}</span><strong>{value}</strong></div>{index < selectedMode.path.length - 1 ? <ArrowRight className={styles.modePathArrow} size={18} /> : null}</div>)}</div></div></div></div></section></Reveal>

      <Reveal><section className={styles.pipelineSection} id="pipeline" aria-labelledby="pipeline-title"><div className={styles.pipelineInner}><div className={styles.workflowIntro}><div><p className={styles.sectionEyebrow}>ONE REALTIME PATH</p><h2 id="pipeline-title">从麦克风到结果，<br /><span>每一步都有位置。</span></h2></div><p>实时音频服务按顺序处理每个 Turn。输出可以是译音、字幕，也可以按配置投递到企业微信。</p></div><div className={styles.pipelineRail}>{pipeline.map(({ label, detail, icon: Icon }, index) => <div className={`${styles.pipelineItem} ${index === 2 ? styles.pipelineItemActive : ""}`} key={label}><div className={styles.pipelineTop}><span>0{index + 1}</span>{index === 2 ? <span className={styles.pipelineLive}>CURRENT</span> : null}</div><Icon size={25} weight="regular" /><h3>{label}</h3><p>{detail}</p></div>)}</div></div></section></Reveal>

      <Reveal><section className={styles.trustSection} id="privacy" aria-labelledby="privacy-title"><div className={styles.trustInner}><div className={styles.trustHeading}><p className={styles.sectionEyebrow}>PRIVACY & DATA</p><h2 id="privacy-title">把数据边界，<span>提前说清楚。</span></h2></div><div className={styles.trustGrid}><div><strong>P0 不保存原始音频</strong><p>实时处理完成后，不把原始语音作为长期档案保存。</p></div><div><strong>保存文本与用量事实</strong><p>长期保存的是文本 Final Turn 和用于统计的用量事实。</p></div><div><strong>字幕和企业微信按配置启用</strong><p>字幕展示与消息投递都是可选输出，不默认打开。</p></div><div><strong>会话是临时会话</strong><p>参与者无需登记，创建会话即可开始面对面交流。</p></div></div></div></section></Reveal>

      <Reveal><section className={styles.contactSection} id="contact"><div className={styles.contactInner}><p className={styles.kicker}>READY WHEN YOU ARE</p><h2>先打开一次 Web 体验，<br /><em>再决定下一步。</em></h2><div className={styles.contactActions}><a className={styles.primaryButton} href={webExperienceHref}>立即打开 Web 体验 <ArrowUpRight size={18} weight="bold" /></a><a className={styles.textButton} href={siteHref("/intro/docs")}>查看文档 <ArrowRight size={18} /></a></div></div></section></Reveal>

      </Localized>
      <SiteFooter />
      <BackToTop />
      </main>
    </LocaleProvider>
  );
}
