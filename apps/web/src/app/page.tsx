import { AuthGate } from "@/features/auth/auth-gate";

export default function Home() {
  return <AuthGate />;
}
