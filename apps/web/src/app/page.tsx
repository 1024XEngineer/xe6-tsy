import { AuthGate } from "@/features/auth/auth-gate";
import IntroPage from "./intro/page";

export default function Home() {
  if (process.env.NEXT_STATIC_EXPORT === "1") return <IntroPage />;
  return <AuthGate />;
}
