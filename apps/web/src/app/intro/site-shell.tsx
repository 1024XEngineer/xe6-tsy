"use client";

import { ArrowUp, ArrowUpRight, CaretDown } from "@phosphor-icons/react";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { siteHref } from "./site-paths";
import styles from "./intro.module.css";

export function SiteNav() {
  return (
    <header className={styles.nav}>
      <a className={styles.wordmark} href={siteHref("/intro")} aria-label="Lingow 首页">
        <span className={styles.wordmarkMark}>L</span>
        <span>Lingow</span>
      </a>
      <div className={styles.navRight}>
        <nav className={styles.navLinks} aria-label="主导航">
          <a href={siteHref("/intro/product")}>产品</a>
          <a href={siteHref("/intro/developer")}>开发者</a>
          <a href={siteHref("/intro/docs")}>文档</a>
          <a href={siteHref("/intro/about")}>关于 Lingow</a>
        </nav>
        <div className={styles.navActions}>
          <button className={styles.languageButton} type="button">
            中文 <CaretDown size={14} weight="bold" />
          </button>
          <a className={styles.navCta} href={siteHref("/intro#contact")}>
            预约体验 <ArrowUpRight size={16} weight="bold" />
          </a>
        </div>
      </div>
    </header>
  );
}

export function SiteFooter() {
  return (
    <footer className={styles.footer}>
      <a className={styles.wordmark} href={siteHref("/intro")}><span className={styles.wordmarkMark}>L</span><span>Lingow</span></a>
      <p>实时语音助手与面对面同传系统。</p>
      <div>
        <a href="https://github.com/1024XEngineer/xe6-tsy" target="_blank" rel="noreferrer">GitHub <ArrowUpRight size={14} /></a>
        <a href={siteHref("/intro/developer")}>开发者入口 <ArrowUpRight size={14} /></a>
      </div>
    </footer>
  );
}

export function BackToTop() {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const onScroll = () => setVisible(window.scrollY > 360);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  return (
    <button
      className={`${styles.backToTop} ${visible ? styles.backToTopVisible : ""}`}
      type="button"
      aria-label="回到顶部"
      title="回到顶部"
      onClick={() => window.scrollTo({ top: 0, behavior: "smooth" })}
    >
      <ArrowUp size={18} weight="bold" />
    </button>
  );
}

export function Reveal({ children, className = "" }: { children: ReactNode; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const element = ref.current;
    if (!element) return;

    element.classList.add(styles.revealPending);

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          element.classList.add(styles.revealVisible);
          observer.unobserve(element);
        }
      },
      { rootMargin: "0px 0px -10% 0px", threshold: 0.08 },
    );

    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  return <div ref={ref} className={`${styles.reveal} ${className}`}>{children}</div>;
}
