import { VoiceExperience } from "@/features/voice/components/voice-experience";
import IntroPage from "./intro/page";

export default function Home() {
  if (process.env.NEXT_STATIC_EXPORT === "1") return <IntroPage />;
  return <VoiceExperience />;
}
