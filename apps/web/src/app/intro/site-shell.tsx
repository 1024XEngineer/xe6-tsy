"use client";

import { ArrowUp, ArrowUpRight, CaretDown } from "@phosphor-icons/react";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { currentSiteNavItem, siteHref, siteNavItems } from "./site-paths";
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
          {siteNavItems.slice(1).map((item) => (
            <a key={item.href} href={siteHref(item.href)}>{item.label}</a>
          ))}
        </nav>
        <MobileNav />
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

function MobileNav() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const navRef = useRef<HTMLDivElement>(null);
  const currentItem = currentSiteNavItem(pathname);

  useEffect(() => {
    if (!open) return;

    const onPointerDown = (event: PointerEvent) => {
      if (!navRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };

    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  return (
    <div ref={navRef} className={styles.mobileNav}>
      <button
        className={styles.mobileNavToggle}
        type="button"
        aria-expanded={open}
        aria-controls="mobile-site-navigation"
        aria-label={`当前页面：${currentItem.label}，${open ? "收起" : "展开"}导航`}
        onClick={() => setOpen((value) => !value)}
      >
        <span>当前：{currentItem.label}</span>
        <CaretDown className={styles.mobileNavToggleIcon} size={16} weight="bold" aria-hidden="true" />
      </button>
      <nav
        id="mobile-site-navigation"
        className={`${styles.mobileNavPanel} ${open ? styles.mobileNavPanelOpen : ""}`}
        aria-label="移动端主导航"
      >
        {siteNavItems.map((item) => {
          const isCurrent = item.href === currentItem.href;
          return (
            <a
              key={item.href}
              className={`${styles.mobileNavLink} ${isCurrent ? styles.mobileNavLinkActive : ""}`}
              href={siteHref(item.href)}
              aria-current={isCurrent ? "page" : undefined}
              onClick={() => setOpen(false)}
            >
              <span>{item.label}</span>
              {isCurrent ? <span className={styles.mobileNavCurrentMark}>当前</span> : null}
            </a>
          );
        })}
      </nav>
    </div>
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
