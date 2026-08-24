"use client";

import {
  ArrowRight,
  ArrowUpRight,
  Check,
  Code,
  DeviceMobile,
  GlobeHemisphereWest,
  Microphone,
  Translate,
  Waveform,
} from "@phosphor-icons/react";
import type { ReactNode } from "react";
import Link from "next/link";

import { BackToTop, Reveal, SiteFooter, SiteNav } from "./site-shell";
import { siteHref } from "./site-paths";
import styles from "./intro.module.css";

export function ProductPage() {
  return (
    <SubpageFrame>
      <SubpageHero eyebrow="PRODUCT / 01" title={<>一个实时系统，<br /><span>两种工作方式。</span></>} copy="从面对面同传到 AI 语音助手，Lingow 复用同一条实时会话，让交流保持在当下。" />
      <Reveal><section className={styles.detailSection}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>01 / MODES</p><h2>根据现场，选择<br /><span>合适的声音入口。</span></h2></div><div className={styles.detailSplit}><DetailPanel label="面对面同传" title="让双方用自己的语言交流。" copy="从语音识别到翻译，再到自然播报，把一场对话里的每个回合连成实时链路。" items={["双语配置与自动语言识别", "流式 ASR、翻译与句末 TTS", "抢话打断与弱网重连"]} /><DetailPanel label="AI 语音助手" title="把下一步交给声音。" copy="唤醒 Lingow，用自然语言完成问答、命令和模式切换，让语音成为更直接的控制入口。" items={["本地唤醒词“小灵小灵”", "自然语言语义命令", "助手问答与实时模式切换"]} /></div></section></Reveal>
      <Reveal><section className={styles.detailSectionAlt}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>02 / PIPELINE</p><h2>每一次回应，<br /><span>都有清晰的路径。</span></h2></div><div className={styles.detailPipeline}>{[{title:"聆听", copy:"捕捉双方语音，保持对话节奏。", icon:Microphone},{title:"识别", copy:"将连续声音转换为可理解的文本。", icon:Waveform},{title:"翻译", copy:"在两个语言槽位之间完成实时转换。", icon:Translate},{title:"传达", copy:"通过语音、字幕或消息传递结果。", icon:ArrowRight}].map(({title,copy,icon:Icon}, index)=><div className={styles.detailPipelineItem} key={title}><span>0{index+1}</span><Icon size={24}/><h3>{title}</h3><p>{copy}</p></div>)}</div></section></Reveal>
      <Reveal><section className={styles.detailSection}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>03 / OUTPUT</p><h2>对话结束之后，<br /><span>结果仍然在。</span></h2></div><div className={styles.outputGrid}><OutputItem title="记录与归属" copy="Final Turn 持久化，支持临时说话人与后续归属修正。" /><OutputItem title="字幕与播报" copy="双向或单向输出，字幕和语音保持同一条状态链路。" /><OutputItem title="可选投递" copy="Email、企业微信与长句字幕投递，按场景启用。" /></div></section></Reveal>
      <Reveal><Boundary /></Reveal>
    </SubpageFrame>
  );
}

export function DeveloperPage() {
  return (
    <SubpageFrame>
      <SubpageHero eyebrow="DEVELOPER / 01" title={<>从 Web Demo，<br /><span>到你的产品。</span></>} copy="通过统一协议、实时事件和设备控制核心，了解 Lingow 如何接入更多终端。" />
      <Reveal><section className={styles.detailSection}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>01 / QUICK START</p><h2>先跑起来，<br /><span>再深入每一层。</span></h2></div><div className={styles.quickStart}><div className={styles.quickStep}><span>01</span><h3>启动 Web 入口</h3><p>使用 Next.js Web 应用查看会话、语言配置、字幕和助手回复。</p><Link href={siteHref("/")} className={styles.textButton}>打开 Web 联调 <ArrowRight size={18}/></Link></div><div className={styles.quickStep}><span>02</span><h3>连接控制面</h3><p>API 服务管理账户、会话、语言配置、记录和实时票据。</p><a href="https://github.com/1024XEngineer/xe6-tsy" target="_blank" rel="noreferrer" className={styles.textButton}>查看仓库 <ArrowUpRight size={18}/></a></div><div className={styles.quickStep}><span>03</span><h3>接入媒体面</h3><p>Realtime Audio 负责 WebRTC、VAD、ASR、翻译、TTS 与运行状态。</p><a href="https://github.com/1024XEngineer/xe6-tsy" target="_blank" rel="noreferrer" className={styles.textButton}>阅读协议 <ArrowUpRight size={18}/></a></div></div></section></Reveal>
      <Reveal><section className={styles.architectureSection}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>02 / ARCHITECTURE</p><h2>一条会话，<br /><span>连接三个世界。</span></h2></div><div className={styles.architectureLarge}><ArchitectureNode icon={GlobeHemisphereWest} title="Web / Mobile / Device" copy="多端控制与交互入口" /><div className={styles.architectureConnector}/><ArchitectureNode icon={Code} title="API Control Plane" copy="账户、会话、记录与语言配置" /><div className={styles.architectureConnector}/><ArchitectureNode icon={Waveform} title="Realtime Audio" copy="WebRTC、VAD、ASR、翻译与 TTS" /><div className={styles.architectureConnector}/><div className={styles.architectureFlow}><span>ASR</span><span>TRANSLATE</span><span>TTS</span></div></div></section></Reveal>
      <Reveal><section className={styles.detailSection}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>03 / CONTRACTS</p><h2>从协议开始，<br /><span>让边界保持清晰。</span></h2></div><div className={styles.contractGrid}><ContractItem title="OpenAPI" copy="REST 接口、账户、会话、语言配置与历史记录。" /><ContractItem title="AsyncAPI" copy="实时状态、字幕、助手回复与错误事件。" /><ContractItem title="Device SDK" copy="设备鉴权、模式命令、唤醒事件和重连控制。" /></div></section></Reveal>
      <Reveal><section className={styles.developerNote}><DeviceMobile size={26}/><div><h3>设备能力正在扩展</h3><p>Device SDK 提供控制核心和接口边界，具体芯片 HAL、WebRTC 与 KWS 模型由平台适配。</p></div></section></Reveal>
    </SubpageFrame>
  );
}

export function AboutPage() {
  return (
    <SubpageFrame>
      <SubpageHero eyebrow="ABOUT LINGOW / 01" title={<>把语言转换，<br /><span>变成对话的连续性。</span></>} copy="Lingow 是面向 Web、移动端和设备控制核心的 AI 语音助手与面对面同传系统。" />
      <Reveal><section className={styles.detailSection}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>01 / PRINCIPLE</p><h2>让技术退后一步，<br /><span>让人回到对话里。</span></h2></div><div className={styles.principleGrid}><Principle icon={Microphone} title="先听见" copy="把连续语音当成对话，而不是一串等待处理的文件。" /><Principle icon={Translate} title="再理解" copy="在双方语言之间建立自然的来回，不打断交流节奏。" /><Principle icon={GlobeHemisphereWest} title="能扩展" copy="用清晰的协议和控制边界连接更多终端与场景。" /></div></section></Reveal>
      <Reveal><section className={styles.detailSectionAlt}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>02 / CURRENT STAGE</p><h2>诚实地展示，<br /><span>正在发生的事情。</span></h2></div><div className={styles.stageGrid}><div><p className={styles.stageNumber}>01</p><h3>公开技术预览</h3><p>Web 是当前主要可运行入口，助手与面对面同传共用实时会话。</p></div><div><p className={styles.stageNumber}>02</p><h3>持续开发</h3><p>移动端控制核心、设备接入和更多交付能力正在逐步完善。</p></div></div></section></Reveal>
      <Reveal><section className={styles.contactSection}><p className={styles.kicker}>OPEN SOURCE PROJECT</p><h2>从公开仓库，<br /><em>开始一次具体的交流。</em></h2><a className={styles.primaryButton} href="https://github.com/1024XEngineer/xe6-tsy" target="_blank" rel="noreferrer">查看 GitHub <ArrowUpRight size={18} weight="bold"/></a></section></Reveal>
    </SubpageFrame>
  );
}

export function DocumentationPage() {
  const sections = [
    { id: "quickstart", number: "01", title: "快速开始", copy: "环境准备、Web 入口与本地联调路径。" },
    { id: "architecture", number: "02", title: "系统架构", copy: "API 控制面、Realtime Audio 媒体面与客户端关系。" },
    { id: "contracts", number: "03", title: "协议与事件", copy: "OpenAPI、AsyncAPI、状态事件和错误码。" },
    { id: "device-sdk", number: "04", title: "Device SDK", copy: "设备鉴权、会话、模式命令与重连控制。" },
  ];

  return (
    <SubpageFrame>
      <section className={styles.docsHero}><p className={styles.kicker}><span className={styles.liveDot}/>LINGOW DOCUMENTATION</p><h1>从第一条命令，<br /><span>开始理解 Lingow。</span></h1><p>文档入口已预留。正文、代码示例和接口细节将在资料确认后逐步补齐。</p></section>
      <div className={styles.docsLayout}>
        <aside className={styles.docsSidebar} aria-label="文档目录"><p>目录</p><nav>{sections.map(section => <a href={`#${section.id}`} key={section.id}><span>{section.number}</span>{section.title}</a>)}</nav></aside>
        <div className={styles.docsContent}>
          {sections.map(section => <Reveal key={section.id}><section className={styles.docsSection} id={section.id}><p className={styles.sectionEyebrow}>{section.number} / {section.title}</p><h2>{section.title}</h2><p>{section.copy}</p><div className={styles.docsPlaceholder}><span>文档内容占位</span><small>正文 / 示例 / 接口说明</small></div></section></Reveal>)}
        </div>
      </div>
    </SubpageFrame>
  );
}

function SubpageFrame({ children }: { children: ReactNode }) {
  return <main className={styles.site}><SiteNav />{children}<SiteFooter /><BackToTop /></main>;
}

function SubpageHero({ eyebrow, title, copy }: { eyebrow: string; title: ReactNode; copy: string }) {
  return <section className={styles.subpageHero}><p className={styles.kicker}><span className={styles.liveDot}/>{eyebrow}</p><h1>{title}</h1><p>{copy}</p></section>;
}

function DetailPanel({ label, title, copy, items }: { label: string; title: string; copy: string; items: string[] }) {
  return <article className={styles.detailPanel}><p className={styles.accentLabel}>{label}</p><h3>{title}</h3><p>{copy}</p><ul>{items.map(item=><li key={item}><Check size={16} weight="bold"/>{item}</li>)}</ul><div className={styles.detailPlaceholder}>产品截图 / 录屏占位</div></article>;
}

function OutputItem({ title, copy }: { title: string; copy: string }) { return <article className={styles.outputItem}><Check size={18} weight="bold"/><h3>{title}</h3><p>{copy}</p></article>; }
function ContractItem({ title, copy }: { title: string; copy: string }) { return <article className={styles.contractItem}><Code size={19}/><h3>{title}</h3><p>{copy}</p><a href="https://github.com/1024XEngineer/xe6-tsy" target="_blank" rel="noreferrer">查看仓库 <ArrowUpRight size={15}/></a></article>; }
function ArchitectureNode({ icon: Icon, title, copy }: { icon: typeof Code; title: string; copy: string }) { return <div className={styles.architectureNodeLarge}><Icon size={21}/><div><strong>{title}</strong><span>{copy}</span></div></div>; }
function Principle({ icon: Icon, title, copy }: { icon: typeof Code; title: string; copy: string }) { return <article className={styles.principleItem}><Icon size={22}/><h3>{title}</h3><p>{copy}</p></article>; }
function Boundary() { return <section className={styles.detailSectionAlt}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>04 / CURRENT SCOPE</p><h2>清楚知道<br /><span>现在能做到什么。</span></h2></div><div className={styles.boundaryGrid}><div><p>介绍页只承诺已经验证的能力，更多终端和集成方向逐步开放。</p></div><ul><li><Check size={16} weight="bold"/>面对面双向交流</li><li><Check size={16} weight="bold"/>Web 主要体验入口</li><li><Check size={16} weight="bold"/>实时语音、翻译与播报</li><li><Check size={16} weight="bold"/>可扩展的协议与设备控制核心</li></ul></div></section>; }
