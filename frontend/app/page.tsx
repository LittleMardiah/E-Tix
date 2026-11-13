import Link from "next/link";
import { CreditCard, Map, Shield } from "lucide-react";

const navLinks = [{ label: "Features", href: "#features" }];

const features = [
  {
    icon: Shield,
    title: "Anti-Bot Ingress",
    description:
      "Queue tokens and rate limits are issued server-side, so scripted buyers never reach live inventory.",
  },
  {
    icon: Map,
    title: "Atomic Group Locking",
    description:
      "A row of seats is held under one lock for a fixed window, releasing itself if the group does not finish checkout.",
  },
  {
    icon: CreditCard,
    title: "Split-Pay Checkout",
    description:
      "One booking, many payers. Every share settles on its own, and the QR is issued once the total is covered.",
  },
];

export default function Home() {
  return (
    <div className="min-h-screen bg-[#FAFAFA] text-[#171717]">
      <header className="border-b border-[#E2E8F0] bg-[#FAFAFA]">
        <div className="relative mx-auto flex h-16 w-full max-w-6xl items-center justify-between px-6">
          <Link
            href="/"
            className="text-base font-bold tracking-tight text-[#171717] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#D08080] focus-visible:ring-offset-2"
          >
            E-Tix Flash
          </Link>

          <nav className="absolute left-1/2 hidden -translate-x-1/2 items-center gap-8 md:flex">
            {navLinks.map((link) => (
              <a
                key={link.label}
                href={link.href}
                className="text-sm text-[#4D4D4D] transition-colors duration-150 hover:text-[#171717]"
              >
                {link.label}
              </a>
            ))}
            <a
              href="https://github.com/LittleMardiah/E-Tix#readme"
              target="_blank"
              rel="noopener noreferrer"
              className="text-sm text-[#4D4D4D] transition-colors duration-150 hover:text-[#171717]"
            >
              Docs
            </a>
          </nav>

          <div className="flex items-center gap-2">
            <Link
              href="/auth/login"
              className="inline-flex h-9 items-center justify-center rounded-md px-3 text-sm font-medium text-[#171717] transition-colors duration-150 hover:bg-[#F1F1F1] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#D08080] focus-visible:ring-offset-2"
            >
              Login
            </Link>
            <Link
              href="/auth/register"
              className="inline-flex h-9 items-center justify-center rounded-md bg-[#D08080] px-4 text-sm font-semibold text-[#171717] transition-colors duration-150 hover:bg-[#C47070] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#D08080] focus-visible:ring-offset-2"
            >
              Get Started
            </Link>
          </div>
        </div>
      </header>

      <main>
        <section className="px-6 py-24">
          <div className="mx-auto max-w-3xl text-center">
            <span className="inline-flex items-center gap-2 rounded-md border border-[#E2E8F0] bg-white px-3 py-1 text-xs font-medium text-[#4D4D4D]">
              <span className="h-1.5 w-1.5 rounded-full bg-[#D08080]" />
              v1.0 is live
            </span>

            <h1 className="mt-6 text-5xl font-semibold tracking-tight text-[#171717] md:text-7xl">
              Smart Ticketing. Zero Scalpers.
            </h1>

            <p className="mx-auto mt-6 max-w-xl text-base leading-relaxed text-[#4D4D4D]">
              Inventory that survives a flash sale: seats are locked atomically
              while your group pays, and every ticket resolves to a single
              dynamic QR at the gate.
            </p>

            <div className="mt-10 flex flex-col items-center justify-center gap-3 sm:flex-row">
              <Link
                href="/auth/register"
                className="inline-flex h-11 w-full items-center justify-center rounded-md bg-[#D08080] px-6 text-sm font-semibold text-[#171717] transition-colors duration-150 hover:bg-[#C47070] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#D08080] focus-visible:ring-offset-2 sm:w-auto"
              >
                Get Started
              </Link>
              <a
                href="#features"
                className="inline-flex h-11 w-full items-center justify-center rounded-md px-6 text-sm font-medium text-[#171717] transition-colors duration-150 hover:bg-[#F1F1F1] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#D08080] focus-visible:ring-offset-2 sm:w-auto"
              >
                Explore Features
              </a>
            </div>
          </div>
        </section>

        <section id="features" className="border-t border-[#E2E8F0] bg-white px-6 py-20">
          <div className="mx-auto max-w-6xl">
            <h2 className="text-3xl font-semibold tracking-tight text-[#171717]">
              Built for the drop
            </h2>
            <p className="mt-3 max-w-xl text-base leading-relaxed text-[#4D4D4D]">
              Three mechanisms do the work that manual moderation cannot.
            </p>

            <div className="mt-10 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
              {features.map((feature) => (
                <div
                  key={feature.title}
                  className="rounded-xl border border-[#E2E8F0] bg-white p-6 transition-colors duration-150 hover:border-[#D08080]"
                >
                  <feature.icon className="h-5 w-5 text-[#D08080]" aria-hidden="true" />
                  <h3 className="mt-4 text-sm font-semibold text-[#171717]">
                    {feature.title}
                  </h3>
                  <p className="mt-2 text-sm leading-relaxed text-[#4D4D4D]">
                    {feature.description}
                  </p>
                </div>
              ))}
            </div>
          </div>
        </section>
      </main>

      <footer className="border-t border-[#E2E8F0] bg-[#FAFAFA] px-6 py-8 text-sm text-[#8F8F8F]">
        <div className="mx-auto flex w-full max-w-6xl flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <p>© 2026 E-Tix Flash</p>
          <p>Smart Ticketing Platform</p>
        </div>
      </footer>
    </div>
  );
}
