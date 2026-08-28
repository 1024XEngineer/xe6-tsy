import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { TransientPhraseSubtitle } from "../model/session";
import { ConversationTranscript } from "./conversation-transcript";

const firstPhrase: TransientPhraseSubtitle = {
  utteranceId: "turn-2",
  phraseSequence: 1,
  sourceText: "今天天气",
  translatedText: "The weather today",
  status: "translated",
};

describe("ConversationTranscript", () => {
  it("updates source and phrase translations inside one live VAD group", () => {
    const { rerender } = render(
      <ConversationTranscript
        activeMode="interpretation"
        assistantReplies={[]}
        asrPartial={{ turnId: "turn-2", text: "今天天气", sourceLanguage: "zh-CN" }}
        phraseSubtitles={[firstPhrase]}
        turns={[]}
      />,
    );

    let transcript = screen.getByRole("region", { name: "同声传译记录" });
    expect(within(transcript).getAllByRole("article")).toHaveLength(1);
    expect(transcript).toHaveTextContent("今天天气");
    expect(transcript).toHaveTextContent("The weather today");

    rerender(
      <ConversationTranscript
        activeMode="interpretation"
        assistantReplies={[]}
        asrPartial={{ turnId: "turn-2", text: "今天天气很好", sourceLanguage: "zh-CN" }}
        phraseSubtitles={[
          firstPhrase,
          {
            utteranceId: "turn-2",
            phraseSequence: 2,
            sourceText: "很好",
            translatedText: " is lovely",
            status: "translated",
          },
        ]}
        turns={[]}
      />,
    );

    transcript = screen.getByRole("region", { name: "同声传译记录" });
    expect(within(transcript).getAllByRole("article")).toHaveLength(1);
    expect(transcript).toHaveTextContent("今天天气很好");
    expect(transcript).toHaveTextContent("The weather today is lovely");
  });

  it("keeps confirmed text bright and the replaceable stash subdued", () => {
    render(
      <ConversationTranscript
        activeMode="interpretation"
        assistantReplies={[]}
        asrPartial={{
          turnId: "turn-2",
          text: "确认，",
          stash: "未确认尾巴",
          sourceLanguage: "zh-CN",
        }}
        phraseSubtitles={[]}
        turns={[]}
      />,
    );

    const transcript = screen.getByRole("region", { name: "同声传译记录" });
    expect(transcript).toHaveTextContent("确认，未确认尾巴");
    expect(within(transcript).getByText("确认，").className).toContain("transcriptConfirmed");
    expect(within(transcript).getByText("未确认尾巴").className).toContain("transcriptStash");
  });

  it("preserves word boundaries for English phrase subtitles", () => {
    render(
      <ConversationTranscript
        activeMode="interpretation"
        assistantReplies={[]}
        asrPartial={null}
        phraseSubtitles={[
          {
            utteranceId: "turn-2",
            phraseSequence: 1,
            sourceText: "Hello,",
            translatedText: "Bonjour,",
            status: "translated",
          },
          {
            utteranceId: "turn-2",
            phraseSequence: 2,
            sourceText: "world",
            translatedText: "monde",
            status: "translated",
          },
        ]}
        turns={[]}
      />,
    );

    const transcript = screen.getByRole("region", { name: "同声传译记录" });
    expect(transcript).toHaveTextContent("Hello, world");
    expect(transcript).toHaveTextContent("Bonjour, monde");
  });

  it("seals the final turn and opens a separate group for the next VAD", () => {
    render(
      <ConversationTranscript
        activeMode="interpretation"
        assistantReplies={[]}
        asrPartial={{ turnId: "turn-2", text: "下一句话", sourceLanguage: "zh-CN" }}
        phraseSubtitles={[
          {
            utteranceId: "turn-2",
            phraseSequence: 1,
            sourceText: "下一句话",
            translatedText: "The next sentence",
            status: "translated",
          },
        ]}
        turns={[
          {
            id: "turn-1",
            sourceLanguage: "中文",
            targetLanguage: "English",
            source: "上一句话",
            translation: "The previous sentence",
          },
        ]}
      />,
    );

    const groups = within(
      screen.getByRole("region", { name: "同声传译记录" }),
    ).getAllByRole("article");
    expect(groups).toHaveLength(2);
    expect(groups[0]).toHaveTextContent("上一句话");
    expect(groups[0]).toHaveTextContent("The previous sentence");
    expect(groups[1]).toHaveTextContent("下一句话");
    expect(groups[1]).toHaveTextContent("The next sentence");
  });

  it("does not mix phrase subtitles from another utterance into the active group", () => {
    render(
      <ConversationTranscript
        activeMode="interpretation"
        assistantReplies={[]}
        asrPartial={{ turnId: "turn-2", text: "当前原文", sourceLanguage: "zh-CN" }}
        phraseSubtitles={[
          {
            utteranceId: "turn-1",
            phraseSequence: 1,
            sourceText: "过期原文",
            translatedText: "Stale translation",
            status: "translated",
          },
          {
            utteranceId: "turn-2",
            phraseSequence: 1,
            sourceText: "当前原文",
            translatedText: "Current translation",
            status: "translated",
          },
        ]}
        turns={[]}
      />,
    );

    const transcript = screen.getByRole("region", { name: "同声传译记录" });
    expect(transcript).toHaveTextContent("Current translation");
    expect(transcript).not.toHaveTextContent("Stale translation");
  });
});
