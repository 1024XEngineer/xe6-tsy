"use client";

import Image from "next/image";
import { ArrowUp, ArrowUpRight, CaretDown } from "@phosphor-icons/react";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { currentSiteNavItem, siteHref, siteNavItems, siteNavItemsEn } from "./site-paths";
import { useLocale } from "./locale";
import styles from "./intro.module.css";

export function SiteNav() {
  const { locale, setLocale } = useLocale();
  const navItems = locale === "en" ? siteNavItemsEn : siteNavItems;
  return (
    <header className={styles.nav}>
      <a className={styles.wordmark} href={siteHref("/intro")} aria-label="Lingow 首页">
        <Image alt="Lingow" className={styles.wordmarkLogo} height={32} priority src="/brand/lingow-logo.svg" width={124} />
      </a>
      <div className={styles.navRight}>
        <nav className={styles.navLinks} aria-label={locale === "en" ? "Main navigation" : "主导航"}>
          {navItems.slice(1).map((item) => (
            <a key={item.href} href={siteHref(item.href)}>{item.label}</a>
          ))}
        </nav>
        <MobileNav />
        <div className={styles.navActions}>
          <button className={styles.languageButton} type="button" aria-label={locale === "en" ? "Switch to Chinese" : "切换到英文"} onClick={() => setLocale(locale === "en" ? "zh" : "en")}>
            <span className={styles.languageLabel}>{locale === "en" ? "中文" : "English"}</span>
          </button>
          <a className={styles.navCta} href={siteHref("/intro#contact")}>
            {locale === "en" ? "Try Lingow" : "预约体验"} <ArrowUpRight size={16} weight="bold" />
          </a>
        </div>
      </div>
    </header>
  );
}

function MobileNav() {
  const { locale } = useLocale();
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const navRef = useRef<HTMLDivElement>(null);
  const currentItem = currentSiteNavItem(pathname);
  const navItems = locale === "en" ? siteNavItemsEn : siteNavItems;
  const currentLabel = navItems.find((item) => item.href === currentItem.href)?.label ?? navItems[0].label;

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
        aria-label={locale === "en" ? `Current page: ${currentLabel}, ${open ? "collapse" : "expand"} navigation` : `当前页面：${currentLabel}，${open ? "收起" : "展开"}导航`}
        onClick={() => setOpen((value) => !value)}
      >
        <span>{locale === "en" ? `Current: ${currentLabel}` : `当前：${currentLabel}`}</span>
        <CaretDown className={styles.mobileNavToggleIcon} size={16} weight="bold" aria-hidden="true" />
      </button>
      <nav
        id="mobile-site-navigation"
        className={`${styles.mobileNavPanel} ${open ? styles.mobileNavPanelOpen : ""}`}
        aria-label={locale === "en" ? "Mobile main navigation" : "移动端主导航"}
      >
        {navItems.map((item) => {
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
              {isCurrent ? <span className={styles.mobileNavCurrentMark}>{locale === "en" ? "Current" : "当前"}</span> : null}
            </a>
          );
        })}
      </nav>
    </div>
  );
}

export function SiteFooter() {
  const { locale } = useLocale();
  return (
    <footer className={styles.footer}>
      <a className={styles.wordmark} href={siteHref("/intro")} aria-label={locale === "en" ? "Lingow home" : "Lingow 首页"}>
        <Image alt="Lingow" className={styles.wordmarkLogo} height={32} src="/brand/lingow-logo.svg" width={124} />
      </a>
      <p>{locale === "en" ? "Real-time voice assistant and face-to-face interpreting system." : "实时语音助手与面对面同传系统。"}</p>
      <div>
        <a href="https://github.com/1024XEngineer/xe6-tsy" target="_blank" rel="noreferrer">GitHub <ArrowUpRight size={14} /></a>
        <a href={siteHref("/intro/docs")}>{locale === "en" ? "Read developer docs" : "阅读开发文档"} <ArrowUpRight size={14} /></a>
      </div>
    </footer>
  );
}

export function BackToTop() {
  const { locale } = useLocale();
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
      aria-label={locale === "en" ? "Back to top" : "回到顶部"}
      title={locale === "en" ? "Back to top" : "回到顶部"}
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
