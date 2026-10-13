export default function Home() {
  const features = [
    {
      title: "Flash Sale Ready",
      desc: "Anti-bot ingress yang sanggup menahan 20k RPS tanpa oversell.",
    },
    {
      title: "Atomic Group Locking",
      desc: "Jamin kursi grup berdampingan dengan penguncian atomik.",
    },
    {
      title: "Split-Pay + Resale",
      desc: "Bayar sendiri-sendiri, resale terkendali dengan fee split.",
    },
  ];

  return (
    <div className="min-h-screen bg-black text-white flex flex-col font-[family-name:var(--font-geist-sans)]">
      <main className="flex-1 flex flex-col items-center justify-center px-6 py-20">
        <section className="w-full max-w-4xl text-center">
          <h1 className="text-5xl sm:text-7xl font-bold tracking-tight bg-gradient-to-b from-white to-neutral-500 bg-clip-text text-transparent">
            E-Tix Flash
          </h1>
          <p className="mt-6 text-lg sm:text-2xl text-neutral-300">
            Smart, Fair &amp; Scalp-Proof Ticketing
          </p>
          <div className="mt-10">
            <a
              href="#"
              className="inline-flex items-center justify-center rounded-full bg-white text-black font-semibold px-8 py-3 text-base transition-colors hover:bg-neutral-200"
            >
              Get Started
            </a>
          </div>
        </section>

        <section className="mt-20 grid w-full max-w-5xl gap-6 sm:grid-cols-3">
          {features.map((f) => (
            <div
              key={f.title}
              className="rounded-2xl border border-neutral-800 bg-neutral-950 p-6 text-left transition-colors hover:border-neutral-600"
            >
              <h2 className="text-xl font-semibold text-white">{f.title}</h2>
              <p className="mt-3 text-sm leading-relaxed text-neutral-400">
                {f.desc}
              </p>
            </div>
          ))}
        </section>
      </main>

      <footer className="w-full py-8 text-center text-sm text-neutral-500">
        &copy; 2026 E-Tix Flash. Portfolio project by M. Arif Aulia.
      </footer>
    </div>
  );
}
