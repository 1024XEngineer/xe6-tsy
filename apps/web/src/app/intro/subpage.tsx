"use client";

import {
  ArrowRight,
  ArrowUpRight,
  Check,
  Code,
  Copy,
  List,
  GlobeHemisphereWest,
  Microphone,
  Translate,
  Waveform,
} from "@phosphor-icons/react";
import type { ReactNode } from "react";
import { useState } from "react";

import { BackToTop, Reveal, SiteFooter, SiteNav } from "./site-shell";
import { siteHref } from "./site-paths";
import styles from "./intro.module.css";

export function ProductPage() {
  return (
    <SubpageFrame>
      <SubpageHero eyebrow="PRODUCT" title={<>一条实时会话，<br /><span>连接两种交流方式。</span></>} copy="Lingow 为面对面交流提供一条临时的实时会话：选择语言对后，可以进行双向同传，也可以将单向译文投递到企业微信。" aside={<HeroAside label="当前会话" title="zh-CN ↔ en-US" items={["临时会话", "双向听译", "企业微信投递"]} />} />
      <Reveal><section className={`${styles.detailSection} ${styles.subpageBandCyan}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>工作方式</p><h2>根据现场，选择<br /><span>合适的声音入口。</span></h2><p>同一条实时会话支持两种方式：面对面同传负责双方互译，AI 助手负责问答、命令和模式切换。</p></div><div className={styles.detailSplit}><DetailPanel label="面对面同传" title="让双方用自己的语言交流。" copy="一场临时会话只需选择一组语言。系统会识别发言、翻译成对方语言，并在句末播报；字幕和消息投递可按需开启。" items={["不要求登录、注册或参与者登记", "语言配置固定在当前 Turn，修改从下一 Turn 生效", "双向听译或单向听译与企业微信投递"]} visual="双语会话 / zh-CN ↔ en-US" /><DetailPanel label="AI 语音助手" title="把下一步交给声音。" copy="Web 入口默认从助手模式开始。唤醒 Lingow 后，可以用自然语言提问、执行命令或切换到同传模式，整个过程不需要重新建立连接。" items={["本地唤醒词“小灵小灵”", "自然语言语义命令与助手问答", "与 interpretation 模式共用一条 WebRTC 会话"]} visual="assistant / interpretation" /></div></div></section></Reveal>
      <Reveal><section className={`${styles.detailSectionAlt} ${styles.subpageBandBlack}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>实时链路</p><h2>每一次回应，<br /><span>都有清晰的路径。</span></h2><p>音频通过 WebRTC 进入实时链路，依次经过 VAD、ASR 和翻译；确认后的结果再进入播报、字幕或消息投递。</p></div><div className={styles.detailPipeline}>{[{title:"聆听", copy:"捕捉双方语音，保持对话节奏。", icon:Microphone},{title:"识别", copy:"将连续声音转换为可理解的文本。", icon:Waveform},{title:"翻译", copy:"在两个语言槽位之间完成实时转换。", icon:Translate},{title:"传达", copy:"通过语音、字幕或消息传递结果。", icon:ArrowRight}].map(({title,copy,icon:Icon})=><div className={styles.detailPipelineItem} key={title}><Icon size={24}/><h3>{title}</h3><p>{copy}</p></div>)}</div></div></section></Reveal>
      <Reveal><section className={`${styles.detailSection} ${styles.subpageBandCyan}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>会话边界</p><h2>轻量开始，<br /><span>明确结束。</span></h2><p>这里关注一场临时交流的完整生命周期，不涉及历史会话管理。</p></div><div className={styles.productSignalGrid}><Signal title="开始" value="选择语言对后获取短期票据，建立 WebRTC 连接。" label="临时会话" /><Signal title="进行中" value="持续听音、处理并输出；新的有效发言可以打断当前译音。" label="运行状态" /><Signal title="结束" value="停止采集和播放，重复结束请求不会产生副作用；服务端确认后释放实时资源。" label="幂等结束" /></div></div></section></Reveal>
      <Reveal><section className={`${styles.detailSectionAlt} ${styles.subpageBandBlack}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>输出与数据</p><h2>把结果送达，<br /><span>不把原始音频留下。</span></h2><p>长期保存的是文本 Final Turn 和用量事实；P0 不保存原始音频，字幕只是可选展示，音频仍是主要交流方式。</p></div><div className={styles.outputGrid}><OutputItem title="双向听译" copy="两个方向都完成识别、翻译、文本记录和目标语言播报。" /><OutputItem title="单向投递" copy="一侧播放译音，另一侧的有效文本结果可异步发送到已绑定的企业微信。" /><OutputItem title="可选展示" copy="有屏幕时展示原文、译文和状态；没有屏幕也能通过音频完成交流。" /></div></div></section></Reveal>
      <Reveal><Boundary /></Reveal>
    </SubpageFrame>
  );
}

export function AboutPage() {
  return (
    <SubpageFrame>
      <SubpageHero eyebrow="ABOUT LINGOW" title={<>让翻译<br /><span>不打断对话。</span></>} copy="Lingow 是一个面向 Web、移动端和设备控制核心的 AI 语音助手与面对面同传系统。我们先把临时交流从开始到结束做得简单清楚，再逐步扩展到更多终端。" aside={<HeroAside label="项目状态" title="Open source" items={["Web 可运行入口", "Mobile 控制核心", "Device 平台适配"]} />} />
      <Reveal><section className={`${styles.detailSection} ${styles.subpageBandCyan}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>产品原则</p><h2>让技术退后一步，<br /><span>让人回到对话里。</span></h2><p>把复杂度留在实时链路和协议边界里，使用者只需专注于眼前这场对话。</p></div><div className={styles.principleGrid}><Principle icon={Microphone} title="先听见" copy="把连续语音当成对话，而不是一串等待处理的文件。" /><Principle icon={Translate} title="再理解" copy="在双方语言之间建立自然的来回，不打断交流节奏。" /><Principle icon={GlobeHemisphereWest} title="能扩展" copy="用清晰的协议和控制边界连接更多终端与场景。" /></div></div></section></Reveal>
      <Reveal><section className={`${styles.architectureSection} ${styles.subpageBandBlack}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>系统架构</p><h2>三层职责，<br /><span>一条会话。</span></h2><p>客户端负责采集、播放和交互；API 管理长期业务状态；Realtime Audio 负责 WebRTC 连接和运行时状态。跨端共享的数据和状态，统一由 contracts 定义。</p></div><div className={styles.aboutArchitecture}><ArchitectureRow icon={GlobeHemisphereWest} title="Web / Mobile / Device" copy="会话配置、字幕、播报、唤醒和控制入口。" /><ArchitectureRow icon={Code} title="API Control Plane" copy="账户、会话、语言配置、Final Turn、用量和异步消息。" /><ArchitectureRow icon={Waveform} title="Realtime Audio" copy="WebRTC、VAD、ASR、翻译、TTS、打断和运行状态。" /><ArchitectureRow icon={Code} title="packages/contracts" copy="REST、信令、实时事件、错误码和状态定义的唯一来源。" /></div></div></section></Reveal>
      <Reveal><section className={`${styles.detailSectionAlt} ${styles.subpageBandCyan}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>当前阶段</p><h2>把项目现状，<br /><span>说明白。</span></h2><p>项目把可运行入口、控制核心和平台适配分开定义，下面按这几个边界说明当前进展。</p></div><div className={styles.stageGrid}><div><p className={styles.stageNumber}>WEB</p><h3>主要可运行入口</h3><p>Next.js Web 负责会话、语言设置、WebRTC、字幕、助手回复和 TTS 交互，默认从 AI 助手模式开始。</p></div><div><p className={styles.stageNumber}>MOBILE</p><h3>控制面核心</h3><p>Mobile 当前提供可编译、可测试的 TypeScript 控制面核心，尚未绑定 UI、PeerConnection 或原生 KWS。</p></div><div><p className={styles.stageNumber}>DEVICE SDK</p><h3>接口而非成品硬件</h3><p>Device SDK 提供鉴权、会话、模式、唤醒事件和重连边界；具体音频 HAL、WebRTC 和 KWS 模型由平台适配。</p></div><div><p className={styles.stageNumber}>OUT OF SCOPE</p><h3>明确不承诺</h3><p>当前不提供管理后台、订单、支付、发票、多人会议同传或硬件制造能力。</p></div></div></div></section></Reveal>
      <Reveal><section className={`${styles.detailSection} ${styles.subpageBandBlack}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>开放资料</p><h2>从公开资料，<br /><span>了解每个决定。</span></h2><p>产品范围、架构和协议都公开在仓库中。可以先从对应文档开始，再进入代码和 Issue。</p></div><div className={styles.aboutResourceGrid}><ResourceLink title="产品需求文档（Issue #302）" label="PRODUCT REQUIREMENTS" href="https://github.com/1024XEngineer/xe6-tsy/issues/302" /><ResourceLink title="架构总览" label="ARCHITECTURE" href="https://github.com/1024XEngineer/xe6-tsy/pull/165" /><ResourceLink title="P0 协议设计" label="P0 CONTRACTS" href="https://github.com/1024XEngineer/xe6-tsy/pull/171" /><ResourceLink title="开发说明" label="DEVELOPMENT" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/docs/DEVELOPMENT.md" /></div></div></section></Reveal>
      <Reveal><section className={`${styles.contactSection} ${styles.subpageContact} ${styles.subpageBandCyan}`}><div className={styles.subpageSectionInner}><p className={styles.kicker}>OPEN SOURCE PROJECT</p><h2>从公开仓库开始，<br /><em>把问题聊具体。</em></h2><a className={styles.primaryButton} href="https://github.com/1024XEngineer/xe6-tsy" target="_blank" rel="noreferrer">查看 GitHub <ArrowUpRight size={18} weight="bold"/></a></div></section></Reveal>
    </SubpageFrame>
  );
}

export function DocumentationPage() {
  const [directoryOpen, setDirectoryOpen] = useState(false);

  return (
    <SubpageFrame>
      <div className={styles.docsActions} aria-label="文档固定操作">
        <a href={siteHref("/")}><ArrowRight size={16} weight="bold" />返回 Web 体验</a>
        <a href="https://github.com/jinyu918/xe6-tsy" target="_blank" rel="noreferrer"><ArrowUpRight size={16} weight="bold" />查看仓库</a>
      </div>
      <div className={styles.docsLayout}>
        <aside className={styles.docsSidebar} aria-label="文档目录">
          <div className={styles.docsSidebarHeader}>
            <p>文档目录</p>
            <button className={styles.docsMenuToggle} type="button" aria-expanded={directoryOpen} aria-controls="docs-navigation" onClick={() => setDirectoryOpen((value) => !value)}>
              <List size={17} weight="bold" />
              <span>{directoryOpen ? "收起目录" : "展开目录"}</span>
            </button>
          </div>
          <nav id="docs-navigation" className={directoryOpen ? styles.docsNavOpen : undefined}>
            <div className={styles.docsNavGroup}>
              <strong>产品</strong>
              <a href="#overview" onClick={() => setDirectoryOpen(false)}>产品概览</a>
              <a href="#capabilities" onClick={() => setDirectoryOpen(false)}>核心能力</a>
              <a href="#scope" onClick={() => setDirectoryOpen(false)}>当前边界</a>
            </div>
            <div className={styles.docsNavGroup}>
              <strong>入门</strong>
              <a href="#quickstart" onClick={() => setDirectoryOpen(false)}>快速开始</a>
            </div>
            <div className={styles.docsNavGroup}>
              <strong>系统</strong>
              <a href="#architecture" onClick={() => setDirectoryOpen(false)}>系统架构</a>
              <a href="#contracts" onClick={() => setDirectoryOpen(false)}>协议与事件</a>
            </div>
            <div className={styles.docsNavGroup}>
              <strong>扩展</strong>
              <a href="#device-sdk" onClick={() => setDirectoryOpen(false)}>Device SDK</a>
            </div>
            <div className={styles.docsNavGroup}>
              <strong>资源</strong>
              <a href="#resources" onClick={() => setDirectoryOpen(false)}>相关文档</a>
            </div>
          </nav>
        </aside>
        <div className={styles.docsContent}>
          <Reveal>
            <section className={styles.docsSection} id="overview">
              <p className={styles.sectionEyebrow}>PRODUCT OVERVIEW</p>
              <h2>产品简介</h2>
              <p>Lingow 面向同一物理空间内的临时双语交流。用户选择一组语言对，使用现有音频终端开始会话，系统在这组语言内识别当前发言、翻译成另一种语言，并通过译音或企业微信投递帮助双方继续交流。</p>
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
            <section className={styles.docsSection} id="capabilities">
              <p className={styles.sectionEyebrow}>CAPABILITIES</p>
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
              <p className={styles.sectionEyebrow}>CURRENT SCOPE</p>
              <h2>当前边界</h2>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" />Web 是当前主要可运行的联调入口，默认以 AI 助手模式创建新会话。</li>
                <li><Check size={16} weight="bold" />Mobile 当前提供可编译、可测试的控制面核心，不包含 UI、PeerConnection 或原生 KWS。</li>
                <li><Check size={16} weight="bold" />Device SDK 提供鉴权、会话、模式、唤醒事件和重连边界，具体音频 HAL、WebRTC 与 KWS 模型由平台适配。</li>
                <li><Check size={16} weight="bold" />管理后台、订单、支付、发票、多人会议同传和硬件制造不属于当前产品范围。</li>
              </ul>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="quickstart">
              <p className={styles.sectionEyebrow}>GETTING STARTED</p>
              <h2>快速开始</h2>
              <p>本地联调由 API、Realtime Audio 与 Web 三部分组成。先启动后端依赖，再启动浏览器入口。</p>
              <div className={styles.docsCallout}><strong>开始之前</strong><p>需要本地可用的 Node.js 环境；完整语音会话还需要 API 与 Realtime Audio 服务。</p></div>
              <div className={styles.docsPrereqGrid} aria-label="前置条件">
                <StatusTag state="required" title="Node.js 22 LTS" detail="Web 入口与 npm 脚本" />
                <StatusTag state="required" title="Go 1.26.7" detail="API 与 Realtime Audio" />
                <StatusTag state="optional" title="Docker Desktop" detail="启动 PostgreSQL / Redis" />
                <StatusTag state="optional" title="真实模型" detail="mock Provider 可离线运行" />
              </div>
              <h3>准备环境</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" />Node.js 22 LTS、npm 与 Go 1.26.7。</li>
                <li><Check size={16} weight="bold" />PostgreSQL 16、Redis/Valkey 7；也可以使用 Docker Desktop 启动它们。</li>
              </ul>
              <h3>配置根环境</h3>
              <p>从仓库根目录复制环境变量示例，并为 API 与 Realtime Audio 配置共享的票据密钥和命令服务凭证。</p>
              <CodeBlock label="根目录 · .env" code={"cp .env.example .env\nLINGOW_SESSION_RUNTIME=enabled\nREALTIME_TICKET_SECRET=<至少 32 字节>\nLINGOW_COMMAND_SYSTEM_TOKEN=<至少 32 字节>\nCOMMAND_LLM_API_KEY=<Qwen API key>\nCOMMAND_LLM_BASE_URL=<Qwen compatible API base URL>"} />
              <p className={styles.docsFootnote}>本地普通音频链路默认使用 mock ASR、翻译和 TTS；要验证真实语音命令，还需要配置 Qwen 意图识别，并将 ASR 切换为可用的真实 Provider。</p>
              <h3>启动本地服务</h3>
              <p>Windows 从仓库根目录启动 API、Realtime Audio 和 Docker 依赖：</p>
              <CodeBlock label="Windows · PowerShell" code={".\\start-local.ps1 -UseDocker"} />
              <p>不使用 Docker 时，脚本会优先连接 `.env` 中的本地 PostgreSQL 和 Redis；也可以通过 <code>-Service api</code> 或 <code>-Service realtime</code> 只启动一个后端服务。</p>
              <p>非 Windows 环境先导入 `.env`，再启动基础依赖和两个 Go 服务：</p>
              <CodeBlock label="Linux · Shell" code={"set -a\n. ./.env\nset +a\ndocker compose -f infra/docker-compose.yml up -d\n(cd services/api && go run .)\n(cd services/realtime-audio && go run .)"} />
              <h3>启动 Web 入口</h3>
              <p>复制 Web 环境变量示例后安装依赖，并启动本地开发服务器。</p>
              <CodeBlock label="Web · Node.js" code={"cd apps/web\ncp .env.example .env.local\nnpm install\nnpm run dev"} />
              <h3>默认地址</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" /><span>Web：<code>http://localhost:3000</code></span></li>
                <li><Check size={16} weight="bold" /><span>API：<code>http://localhost:8080</code></span></li>
                <li><Check size={16} weight="bold" /><span>Realtime Audio：<code>http://localhost:8090</code></span></li>
                <li><Check size={16} weight="bold" /><span>PostgreSQL：<code>localhost:5432</code>；Redis/Valkey：<code>localhost:6379</code></span></li>
              </ul>
              <p className={styles.docsFootnote}>Web 默认以 AI 助手模式创建会话；可通过 <code>NEXT_PUBLIC_LINGOW_INITIAL_MODE</code> 选择同传入口。</p>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="architecture">
              <p className={styles.sectionEyebrow}>ARCHITECTURE</p>
              <h2>系统架构</h2>
              <p>Lingow 将客户端、API 控制面和 Realtime Audio 媒体面分开：客户端只负责采集、播放和交互，API 拥有长期业务状态，Realtime Audio 拥有实时连接与运行状态。</p>
              <div className={styles.docsFlow}>
                <div><span>01</span><strong>Web / Mobile / Device</strong><p>会话配置、字幕、播报与控制入口。</p></div>
                <div><span>02</span><strong>API Control Plane</strong><p>账户、会话、语言配置与实时连接票据。</p></div>
                <div><span>03</span><strong>Realtime Audio</strong><p>WebRTC、VAD、ASR、翻译、TTS 与运行状态。</p></div>
                <div><span>04</span><strong>packages/contracts</strong><p>统一维护 REST、信令、实时事件、错误码和状态定义。</p></div>
                <div><span>05</span><strong>infra</strong><p>提供 PostgreSQL、Redis/Valkey 和可选的 realtime 会话哈希网关。</p></div>
              </div>
              <h3>一条实时链路</h3>
              <CodeBlock label="Runtime flow" code={"Web / Mobile / Device\n  -> API: account / session / language config / realtime ticket\n  -> Realtime Audio: WebRTC signaling / audio / control events\n  -> VAD -> ASR -> translation -> TTS or message delivery\n  -> API: Final Turn / usage / history / asynchronous messages"} />
              <h3>一条会话的职责边界</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" />Web 负责用户交互、会话 API 调用与字幕、TTS 呈现。</li>
                <li><Check size={16} weight="bold" />API 负责账户、会话、语言配置、Final Turn、用量和异步消息等长期业务状态。</li>
                <li><Check size={16} weight="bold" />Realtime Audio 负责 WebRTC 连接、VAD、ASR、翻译、TTS、打断和运行时状态机。</li>
                <li><Check size={16} weight="bold" />API 只查询实时状态快照，不重复维护实时播放状态机；跨服务数据必须通过 contracts 并支持幂等、重试和可靠投递。</li>
              </ul>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="contracts">
              <p className={styles.sectionEyebrow}>CONTRACTS</p>
              <h2>协议与事件</h2>
              <p>控制面与实时面使用不同契约表达各自的边界。它们由同一个会话状态连接，但不重复维护媒体运行态。</p>
              <div className={styles.docsContractGrid}>
                <article><span>REST</span><h3>OpenAPI</h3><p>账户、会话、语言配置、历史记录与连接票据。</p></article>
                <article><span>EVENTS</span><h3>AsyncAPI</h3><p>运行状态、字幕、助手回复、模式切换与错误事件。</p></article>
                <article><span>LANGUAGE</span><h3>Go / TypeScript</h3><p>共享 realtime、records、语言配置和错误码类型，避免各端重复定义 DTO。</p></article>
              </div>
              <h3>契约唯一来源</h3>
              <p><code>packages/contracts</code> 同时维护 OpenAPI、实时事件、WebRTC 信令边界、错误码、会话状态机以及 Go/TypeScript 绑定。跨端字段先改契约，再改 API、Realtime Audio、Web 或 Device SDK。</p>
              <h3>实时连接票据</h3>
              <p>正式联调由 API 为已创建的语音会话签发实时连接票据。Web 在建立 WebRTC 连接前获取该票据。</p>
              <CodeBlock label="REST · POST" code={"POST /api/v1/voice-sessions/{id}/realtime-ticket"} />
              <h3>媒体与事件方向</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" />音频媒体使用 WebRTC audio track，不通过 WebSocket 传输。</li>
                <li><Check size={16} weight="bold" />WebRTC DataChannel 或 realtime HTTP 接口承载控制事件；API 不交换 SDP/ICE。</li>
                <li><Check size={16} weight="bold" /><span>核心事件包括 <code>session.start</code>、<code>language.selected</code>、<code>wake_word.detected</code>、<code>command.result</code>、<code>webrtc.connected</code>、<code>asr.partial</code>、<code>asr.final</code>、<code>translation.final</code>、<code>tts.ready</code>、<code>playback.start</code>、<code>playback.stop</code>、<code>session.end</code> 和 <code>error</code>。</span></li>
              </ul>
              <p className={styles.docsFootnote}>本地开发旁路仅用于显式启用的 <code>next dev</code> 环境，不应用于生产部署。</p>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="device-sdk">
              <p className={styles.sectionEyebrow}>DEVICE SDK</p>
              <h2>设备接入边界</h2>
              <p>Device SDK 是面向硬件厂商和方案商的 Go 控制核心参考实现，提供会话、模式命令、唤醒事件、状态投影与弱网重连边界；具体芯片音频 HAL、WebRTC 和 KWS 模型由设备平台适配。</p>
              <div className={styles.docsCallout}><strong>保持同一条会话</strong><p>唤醒与模式切换应复用现有 Runtime 和 WebRTC 连接，不应为每次语音命令重建连接。</p></div>
              <h3>当前能力</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" /><span><code>ModeController</code> 通过 <code>GET/POST /realtime/v1/sessions/{'{session_id}'}/mode</code> 读取和切换模式，并携带 runtime 与 generation 进行冲突保护。</span></li>
                <li><Check size={16} weight="bold" /><span><code>StateStore</code> 过滤迟到快照，<code>Reconnector</code> 通过平台注入的策略恢复连接。</span></li>
                <li><Check size={16} weight="bold" /><span><code>SessionStartClient</code> 发送类型化 <code>initial_mode</code>；省略时显式使用 <code>interpretation</code>。</span></li>
                <li><Check size={16} weight="bold" /><span><code>DeviceAuthClient</code> 使用设备身份和 Ed25519 私钥完成挑战签名，换取短期 device token。</span></li>
              </ul>
              <h3>事件方向</h3>
              <CodeBlock label="Device events" code={"device -> api:\n  device.pair / device-auth.challenge / device-auth.token\n  session.start / realtime_ticket.request / session.end\n\ndevice -> realtime-audio:\n  webrtc.offer / ice.candidate / audio track / wake_word.detected\n\nrealtime-audio -> device:\n  webrtc.answer / ice.candidate / runtime.snapshot / mode.snapshot\n  asr.partial / asr.final / translation.final\n  playback.start / playback.stop / error / command.result"} />
              <h3>平台适配边界</h3>
              <ul className={styles.docsList}>
                <li><Check size={16} weight="bold" /><span>固定唤醒词“小灵小灵”命中后只发送 <code>wake_word.detected</code>，设备不解析业务命令，也不把模型名、阈值、目标模式或语言方向放进事件。</span></li>
                <li><Check size={16} weight="bold" /><span>同一份麦克风 PCM 持续进入板载 KWS 和既有 WebRTC 编码链路；KWS、音频 HAL、PeerConnection 生命周期由平台实现。</span></li>
                <li><Check size={16} weight="bold" /><span>设备不能保存用户 Access Token 或 Refresh Token；生产固件使用 <code>/api/v1/device/voice-sessions/*</code>，只在短期 device token 有效时请求 realtime ticket。</span></li>
              </ul>
            </section>
          </Reveal>

          <Reveal>
            <section className={styles.docsSection} id="resources">
              <p className={styles.sectionEyebrow}>RESOURCES</p>
              <h2>相关文档</h2>
              <p>从系统结构、开发约定到协议和产品范围，按需要打开对应的设计资料。</p>
              <div className={styles.docsResourceGroups}>
                <ResourceGroup title="README / 开发说明">
                  <ResourceLink title="仓库 README" label="README" href="https://github.com/jinyu918/xe6-tsy" />
                  <ResourceLink title="开发说明" label="DEVELOPMENT" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/docs/DEVELOPMENT.md" />
                  <ResourceLink title="产品需求文档（PRD）" label="PRODUCT REQUIREMENTS" href="https://github.com/1024XEngineer/xe6-tsy/issues/302" />
                </ResourceGroup>
                <ResourceGroup title="协议 / Contracts">
                  <ResourceLink title="OpenAPI REST 契约" label="OPENAPI" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/packages/contracts/openapi.yaml" />
                  <ResourceLink title="AsyncAPI 实时事件" label="ASYNCAPI" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/packages/contracts/events.yaml" />
                  <ResourceLink title="跨端契约说明" label="CONTRACTS README" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/packages/contracts/README.md" />
                </ResourceGroup>
                <ResourceGroup title="设备 / 部署">
                  <ResourceLink title="Device SDK 接入说明" label="DEVICE SDK" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/sdks/device/README.md" />
                  <ResourceLink title="生产部署文档" label="DEPLOYMENT" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/infra/production/README.md" />
                  <ResourceLink title="本地基础设施说明" label="INFRA" href="https://github.com/1024XEngineer/xe6-tsy/blob/main/infra/README.md" />
                </ResourceGroup>
                <ResourceGroup title="设计与范围">
                  <ResourceLink title="Lingow 架构总览" label="ARCHITECTURE" href="https://github.com/1024XEngineer/xe6-tsy/pull/165" />
                  <ResourceLink title="Lingow 模块详细设计" label="MODULE DESIGN" href="https://github.com/1024XEngineer/xe6-tsy/pull/169" />
                  <ResourceLink title="Lingow P0 协议设计" label="P0 CONTRACTS" href="https://github.com/1024XEngineer/xe6-tsy/pull/171" />
                </ResourceGroup>
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

function SubpageHero({ eyebrow, title, copy, aside }: { eyebrow: string; title: ReactNode; copy: string; aside: ReactNode }) {
  return <section className={`${styles.subpageHero} ${styles.subpageBandBlack}`}><div className={styles.subpageHeroInner}><div className={styles.subpageHeroBody}><p className={styles.kicker}><span className={styles.liveDot}/>{eyebrow}</p><h1>{title}</h1><p>{copy}</p></div>{aside}</div></section>;
}

function HeroAside({ label, title, items }: { label: string; title: string; items: string[] }) {
  return <aside className={styles.subpageHeroAside}><span>{label}</span><strong>{title}</strong><ul>{items.map(item => <li key={item}>{item}</li>)}</ul></aside>;
}

function DetailPanel({ label, title, copy, items, visual }: { label: string; title: string; copy: string; items: string[]; visual: string }) {
  return <article className={styles.detailPanel}><p className={styles.accentLabel}>{label}</p><h3>{title}</h3><p>{copy}</p><ul>{items.map(item=><li key={item}><Check size={16} weight="bold"/>{item}</li>)}</ul><div className={styles.detailPlaceholder}><span>{visual}</span><small>一场会话 · 一个事实来源</small></div></article>;
}

function OutputItem({ title, copy }: { title: string; copy: string }) { return <article className={styles.outputItem}><Check size={18} weight="bold"/><h3>{title}</h3><p>{copy}</p></article>; }
function Principle({ icon: Icon, title, copy }: { icon: typeof Code; title: string; copy: string }) { return <article className={styles.principleItem}><Icon size={22}/><h3>{title}</h3><p>{copy}</p></article>; }
function ResourceGroup({ title, children }: { title: string; children: ReactNode }) { return <section className={styles.docsResourceGroup}><h3>{title}</h3><div className={styles.docsResourceGrid}>{children}</div></section>; }
function StatusTag({ state, title, detail }: { state: "required" | "optional"; title: string; detail: string }) { return <div className={styles.docsStatusTag}><span className={`${styles.docsStatusPill} ${state === "required" ? styles.docsStatusRequired : styles.docsStatusOptional}`}>{state === "required" ? "必需" : "可选"}</span><strong>{title}</strong><small>{detail}</small></div>; }
function CodeBlock({ code, label }: { code: string; label: string }) {
  const [status, setStatus] = useState<"idle" | "copied" | "error">("idle");

  const copyCode = async () => {
    try {
      if (!navigator.clipboard) throw new Error("Clipboard API unavailable");
      await navigator.clipboard.writeText(code);
      setStatus("copied");
    } catch {
      setStatus("error");
    }
  };

  return <div className={styles.docsCodeShell}><div className={styles.docsCodeToolbar}><span>{label}</span><button type="button" onClick={copyCode} aria-label={status === "copied" ? `${label}已复制` : `复制${label}`}><Copy size={15} weight="bold" /><span>{status === "copied" ? "已复制" : status === "error" ? "复制失败" : "复制"}</span></button></div><pre className={styles.docsCodeBlock}><code>{code}</code></pre></div>;
}
function ResourceLink({ title, label, href }: { title: string; label: string; href: string }) { return <a className={styles.docsResourceLink} href={href} target="_blank" rel="noreferrer"><span>{label}</span><strong>{title}</strong><ArrowUpRight size={17}/></a>; }
function Signal({ title, value, label }: { title: string; value: string; label: string }) { return <article className={styles.productSignal}><span>{label}</span><h3>{title}</h3><p>{value}</p></article>; }
function ArchitectureRow({ icon: Icon, title, copy }: { icon: typeof Code; title: string; copy: string }) { return <article className={styles.aboutArchitectureRow}><Icon size={22}/><div><h3>{title}</h3><p>{copy}</p></div><ArrowRight size={18} aria-hidden="true" /></article>; }
function Boundary() { return <section className={`${styles.detailSection} ${styles.subpageBandCyan}`}><div className={styles.subpageSectionInner}><div className={styles.detailSectionHeader}><p className={styles.sectionEyebrow}>当前范围</p><h2>清楚知道<br /><span>现在能做到什么。</span></h2></div><div className={styles.boundaryGrid}><div><p>介绍页只承诺已经验证的能力，更多终端和集成会逐步开放。P0 关注的是当前这一场交流，而不是长期档案或后台工作区。</p></div><ul><li><Check size={16} weight="bold"/>临时双语会话与双向听译</li><li><Check size={16} weight="bold"/>Web 主要体验入口</li><li><Check size={16} weight="bold"/>实时语音、翻译与句末播报</li><li><Check size={16} weight="bold"/>可扩展的协议与设备控制核心</li></ul></div></div></section>; }
