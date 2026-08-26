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
  return (
    <SubpageFrame>
      <section className={styles.docsHero}>
        <p className={styles.kicker}><span className={styles.liveDot}/>LINGOW DOCUMENTATION</p>
        <h1>从第一条命令，<br /><span>开始理解 Lingow。</span></h1>
        <p>从本地联调到实时会话边界，按运行路径阅读 Lingow 的 Web、控制面和媒体面。</p>
      </section>
      <div className={styles.docsLayout}>
        <aside className={styles.docsSidebar} aria-label="文档目录">
          <p>文档目录</p>
          <nav>
            <div className={styles.docsNavGroup}>
              <strong>产品</strong>
              <a href="#overview"><span>01</span>产品概览</a>
              <a href="#capabilities"><span>06</span>核心能力</a>
              <a href="#scope"><span>07</span>当前边界</a>
            </div>
            <div className={styles.docsNavGroup}>
              <strong>入门</strong>
              <a href="#quickstart"><span>02</span>快速开始</a>
            </div>
            <div className={styles.docsNavGroup}>
              <strong>系统</strong>
              <a href="#architecture"><span>03</span>系统架构</a>
              <a href="#contracts"><span>04</span>协议与事件</a>
            </div>
            <div className={styles.docsNavGroup}>
              <strong>扩展</strong>
              <a href="#device-sdk"><span>05</span>Device SDK</a>
            </div>
            <div className={styles.docsNavGroup}>
              <strong>资源</strong>
              <a href="#resources"><span>08</span>相关文档</a>
            </div>
          </nav>
          <a className={styles.docsRepositoryLink} href="https://github.com/jinyu918/xe6-tsy" target="_blank" rel="noreferrer">查看仓库 <ArrowUpRight size={15}/></a>
        </aside>
        <div className={styles.docsContent}>
          <Reveal>
            <section className={styles.docsSection} id="overview">
              <p className={styles.sectionEyebrow}>01 / PRODUCT OVERVIEW</p>
              <h2>先理解产品，<br /><span>再理解技术。</span></h2>
              <p>Lingow 面向同一物理空间内的临时双语交流。用户选择一组语言对，使用现有音频终端开始会话，系统在这组语言内识别当前发言、翻译成另一种语言，并通过译音或企业微信投递帮助双方继续交流。</p>
              <div className={styles.docsCallout}><strong>P0 产品目标</strong><p>不要求登录、注册或参与者登记，让通常为两人的临时交流完成稳定的双语听译闭环。字幕是可选展示，音频是主链路。</p></div>
              <h3>它解决什么问题</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" />双方没有共同语言时，仍能在同一场对话中自然来回交流。</li>
                <li><Check size={16} weight="bold" />交流是临时发生的，不需要先登录、注册或建立参与者资料。</li>
                <li><Check size={16} weight="bold" />译音过长时，可通过单向输出和异步投递减少对交流节奏的阻塞。</li>
                <li><Check size={16} weight="bold" />语言可能在交流中变化，会话内修改配置并从下一 Turn 生效。</li>
              </ul>
              <h3>两种输出方式</h3>
              <div className={styles.docsContractGrid}>
                <article><span>BIDIRECTIONAL</span><h3>双向听译</h3><p>双方都听到目标语言译音，每个有效 Turn 都经过识别、翻译、Final Turn 保存和 TTS。</p></article>
                <article><span>ONE-WAY + DELIVERY</span><h3>单向听译与投递</h3><p>一侧播放译音，另一侧的有效 Final Turn 异步发送到已绑定的企业微信目标。</p></article>
              </div>
              <h3>一次会话如何开始</h3>
              <div className={styles.docsFlow}>
                <div><span>01</span><strong>选择语言对与输出方式</strong><p>在会话开始前确定当前支持的语言范围。</p></div>
                <div><span>02</span><strong>创建临时会话并建立实时连接</strong><p>API 签发短期票据，Realtime Audio 建立 WebRTC 链路。</p></div>
                <div><span>03</span><strong>听音、处理、播放或投递</strong><p>每轮语音完成 VAD、ASR、翻译，再按输出配置继续。</p></div>
                <div><span>04</span><strong>结束并释放资源</strong><p>客户端停止采集和播放，服务端幂等结束并回收实时资源。</p></div>
              </div>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="quickstart">
              <p className={styles.sectionEyebrow}>02 / GETTING STARTED</p>
              <h2>快速开始</h2>
              <p>本地联调由 API、Realtime Audio 与 Web 三部分组成。先启动后端依赖，再启动浏览器入口。</p>
              <div className={styles.docsCallout}><strong>开始之前</strong><p>需要本地可用的 Node.js 环境；完整语音会话还需要 API 与 Realtime Audio 服务。</p></div>
              <h3>启动本地服务</h3>
              <p>在仓库根目录运行启动脚本，默认会启动 API 与 Realtime Audio。</p>
              <pre className={styles.docsCodeBlock}><code>{".\\start-local.ps1"}</code></pre>
              <h3>启动 Web 入口</h3>
              <p>复制 Web 环境变量示例后安装依赖，并启动本地开发服务器。</p>
              <pre className={styles.docsCodeBlock}><code>{"cd apps/web\ncp .env.example .env.local\nnpm install\nnpm run dev"}</code></pre>
              <p className={styles.docsFootnote}>Web 默认以 AI 助手模式创建会话；可通过 <code>NEXT_PUBLIC_LINGOW_INITIAL_MODE</code> 选择同传入口。</p>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="architecture">
              <p className={styles.sectionEyebrow}>03 / ARCHITECTURE</p>
              <h2>系统架构</h2>
              <p>Lingow 把产品交互、业务会话和实时媒体处理分在三个清晰的职责层中。</p>
              <div className={styles.docsFlow}>
                <div><span>01</span><strong>Web / Mobile / Device</strong><p>会话配置、字幕、播报与控制入口。</p></div>
                <div><span>02</span><strong>API Control Plane</strong><p>账户、会话、语言配置与实时连接票据。</p></div>
                <div><span>03</span><strong>Realtime Audio</strong><p>WebRTC、VAD、ASR、翻译、TTS 与运行状态。</p></div>
              </div>
              <h3>一条会话的职责边界</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" />Web 负责用户交互、会话 API 调用与字幕、TTS 呈现。</li>
                <li><Check size={16} weight="bold" />API 负责业务会话、配置与实时连接票据。</li>
                <li><Check size={16} weight="bold" />Realtime Audio 负责 WebRTC 连接和运行时状态机。</li>
              </ul>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="contracts">
              <p className={styles.sectionEyebrow}>04 / CONTRACTS</p>
              <h2>协议与事件</h2>
              <p>控制面与实时面使用不同契约表达各自的边界。它们由同一个会话状态连接，但不重复维护媒体运行态。</p>
              <div className={styles.docsContractGrid}>
                <article><span>REST</span><h3>OpenAPI</h3><p>账户、会话、语言配置、历史记录与连接票据。</p></article>
                <article><span>EVENTS</span><h3>AsyncAPI</h3><p>运行状态、字幕、助手回复、模式切换与错误事件。</p></article>
              </div>
              <h3>实时连接票据</h3>
              <p>正式联调由 API 为已创建的语音会话签发实时连接票据。Web 在建立 WebRTC 连接前获取该票据。</p>
              <pre className={styles.docsCodeBlock}><code>{"POST /api/v1/voice-sessions/{id}/realtime-ticket"}</code></pre>
              <p className={styles.docsFootnote}>本地开发旁路仅用于显式启用的 <code>next dev</code> 环境，不应用于生产部署。</p>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="device-sdk">
              <p className={styles.sectionEyebrow}>05 / DEVICE SDK</p>
              <h2>设备接入边界</h2>
              <p>Device SDK 提供会话、模式命令、唤醒事件与重连控制的边界；具体芯片音频 HAL、WebRTC 与 KWS 模型由设备平台适配。</p>
              <div className={styles.docsCallout}><strong>保持同一条会话</strong><p>唤醒与模式切换应复用现有 Runtime 和 WebRTC 连接，不应为每次语音命令重建连接。</p></div>
              <h3>当前接入原则</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" />设备发送统一的 <code>wake_word.detected</code> 契约。</li>
                <li><Check size={16} weight="bold" />自然语言命令沿用当前 WebRTC 音轨进入实时服务。</li>
                <li><Check size={16} weight="bold" />模型文件、阈值和本地推理运行时由客户端负责。</li>
              </ul>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="capabilities">
              <p className={styles.sectionEyebrow}>06 / CAPABILITIES</p>
              <h2>核心能力</h2>
              <p>Lingow 将语音入口、实时媒体链路和可扩展的设备边界组合成一条会话体验。</p>
              <div className={styles.docsCapabilityGrid}>
                <article><span>01</span><h3>AI 语音助手</h3><p>支持唤醒词检测、自然语言命令、助手问答与模式切换。</p></article>
                <article><span>02</span><h3>面对面同传</h3><p>在选定语言对内完成自动语言识别、流式 ASR、翻译与句末 TTS。</p></article>
                <article><span>03</span><h3>实时会话</h3><p>通过 WebRTC 音频、可靠有序 DataChannel、运行状态和模式快照保持链路连续。</p></article>
                <article><span>04</span><h3>记录与投递</h3><p>保存文本 Final Turn 和用量事实，并按配置支持 Email、企业微信等异步投递。</p></article>
              </div>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="scope">
              <p className={styles.sectionEyebrow}>07 / CURRENT SCOPE</p>
              <h2>当前边界</h2>
              <p>以下边界来自当前仓库实现和 PRD，帮助接入者区分已可联调能力与仍在演进的方向。</p>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" />Web 是当前主要可运行的联调入口，默认以 AI 助手模式创建新会话。</li>
                <li><Check size={16} weight="bold" />Mobile 当前提供可编译、可测试的控制面核心，不包含 UI、PeerConnection 或原生 KWS。</li>
                <li><Check size={16} weight="bold" />Device SDK 提供鉴权、会话、模式、唤醒事件和重连边界，具体音频 HAL、WebRTC 与 KWS 模型由平台适配。</li>
                <li><Check size={16} weight="bold" />管理后台、订单、支付、发票、多人会议同传和硬件制造不属于当前产品范围。</li>
              </ul>
              <div className={styles.docsCallout}><strong>P0 产品闭环</strong><p>当前优先验证免登录临时会话、双语听译、播放控制、失败恢复和不重复投递；账户化与长句增强属于后续阶段。</p></div>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="resources">
              <p className={styles.sectionEyebrow}>08 / RESOURCES</p>
              <h2>相关文档</h2>
              <p>从系统结构、开发约定到协议和产品范围，按需要打开对应的设计资料。</p>
              <div className={styles.docsResourceGrid}>
                <ResourceLink title="Lingow 架构总览" label="ARCHITECTURE" href="https://github.com/1024XEngineer/xe6-tsy/pull/165" />
                <ResourceLink title="开发说明" label="DEVELOPMENT" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/docs/DEVELOPMENT.md" />
                <ResourceLink title="Lingow 模块详细设计" label="MODULE DESIGN" href="https://github.com/1024XEngineer/xe6-tsy/pull/169" />
                <ResourceLink title="Lingow P0 协议设计" label="P0 CONTRACTS" href="https://github.com/1024XEngineer/xe6-tsy/pull/171" />
                <ResourceLink title="产品需求文档（PRD）" label="PRODUCT REQUIREMENTS" href="https://github.com/1024XEngineer/xe6-tsy/issues/302" />
              </div>
            </section>
          </Reveal>
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
function ResourceLink({ title, label, href }: { title: string; label: string; href: string }) { return <a className={styles.docsResourceLink} href={href} target="_blank" rel="noreferrer"><span>{label}</span><strong>{title}</strong><ArrowUpRight size={17}/></a>; }
function Boundary() { return <section className={styles.detailSectionAlt}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>04 / CURRENT SCOPE</p><h2>清楚知道<br /><span>现在能做到什么。</span></h2></div><div className={styles.boundaryGrid}><div><p>介绍页只承诺已经验证的能力，更多终端和集成方向逐步开放。</p></div><ul><li><Check size={16} weight="bold"/>面对面双向交流</li><li><Check size={16} weight="bold"/>Web 主要体验入口</li><li><Check size={16} weight="bold"/>实时语音、翻译与播报</li><li><Check size={16} weight="bold"/>可扩展的协议与设备控制核心</li></ul></div></section>; }
