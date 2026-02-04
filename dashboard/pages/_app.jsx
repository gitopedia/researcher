import { useState, useEffect } from 'react';
import Head from 'next/head';
import Link from 'next/link';
import { useRouter } from 'next/router';

export default function App({ Component, pageProps }) {
  const router = useRouter();
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
  }, []);

  const navItems = [
    { href: '/', label: 'Dashboard', icon: '📊' },
    { href: '/researcher', label: 'Researcher', icon: '🔬' },
    { href: '/topics', label: 'Topics', icon: '📝' },
    { href: '/images', label: 'Images', icon: '🖼️' },
    { href: '/logs', label: 'Logs', icon: '📜' },
  ];

  return (
    <>
      <Head>
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <title>Researcher Dashboard</title>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        <link 
          href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600&family=Inter:wght@400;500;600;700&display=swap" 
          rel="stylesheet" 
        />
      </Head>
      
      <style jsx global>{`
        :root {
          --bg-primary: #0a0a0f;
          --bg-secondary: #12121a;
          --bg-card: #1a1a24;
          --bg-hover: #22222e;
          --border-color: #2a2a38;
          --text-primary: #e8e8ed;
          --text-secondary: #9898a8;
          --text-muted: #68687a;
          --accent-green: #22c55e;
          --accent-red: #ef4444;
          --accent-yellow: #eab308;
          --accent-blue: #3b82f6;
          --accent-purple: #8b5cf6;
        }

        * {
          margin: 0;
          padding: 0;
          box-sizing: border-box;
        }

        html, body {
          background-color: var(--bg-primary);
          color: var(--text-primary);
          font-family: 'Inter', system-ui, -apple-system, sans-serif;
          line-height: 1.5;
          min-height: 100vh;
        }

        a {
          color: var(--accent-blue);
          text-decoration: none;
        }

        a:hover {
          text-decoration: underline;
        }

        button {
          font-family: inherit;
          cursor: pointer;
        }

        code, pre {
          font-family: 'JetBrains Mono', monospace;
        }

        /* Scrollbar styling */
        ::-webkit-scrollbar {
          width: 8px;
          height: 8px;
        }
        ::-webkit-scrollbar-track {
          background: var(--bg-secondary);
        }
        ::-webkit-scrollbar-thumb {
          background: var(--border-color);
          border-radius: 4px;
        }
        ::-webkit-scrollbar-thumb:hover {
          background: var(--text-muted);
        }
      `}</style>

      <div style={{ display: 'flex', minHeight: '100vh' }}>
        {/* Sidebar Navigation */}
        <nav style={{
          width: 220,
          backgroundColor: 'var(--bg-secondary)',
          borderRight: '1px solid var(--border-color)',
          padding: '20px 0',
          position: 'fixed',
          height: '100vh',
          overflowY: 'auto',
        }}>
          <div style={{
            padding: '0 20px 24px',
            borderBottom: '1px solid var(--border-color)',
            marginBottom: 16,
          }}>
            <h1 style={{
              fontSize: '1.1rem',
              fontWeight: 700,
              color: 'var(--text-primary)',
              letterSpacing: '-0.02em',
            }}>
              🔬 Researcher
            </h1>
            <p style={{
              fontSize: '0.75rem',
              color: 'var(--text-muted)',
              marginTop: 4,
            }}>
              Dashboard
            </p>
          </div>

          <ul style={{ listStyle: 'none' }}>
            {navItems.map(item => {
              const isActive = router.pathname === item.href;
              return (
                <li key={item.href}>
                  <Link href={item.href} style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 10,
                    padding: '10px 20px',
                    color: isActive ? 'var(--text-primary)' : 'var(--text-secondary)',
                    backgroundColor: isActive ? 'var(--bg-hover)' : 'transparent',
                    borderLeft: isActive ? '3px solid var(--accent-blue)' : '3px solid transparent',
                    fontSize: '0.9rem',
                    fontWeight: isActive ? 500 : 400,
                    textDecoration: 'none',
                    transition: 'all 0.15s ease',
                  }}>
                    <span>{item.icon}</span>
                    <span>{item.label}</span>
                  </Link>
                </li>
              );
            })}
          </ul>
        </nav>

        {/* Main Content */}
        <main style={{
          flex: 1,
          marginLeft: 220,
          padding: 24,
          minHeight: '100vh',
        }}>
          {mounted && <Component {...pageProps} />}
        </main>
      </div>
    </>
  );
}
