import { useState, useEffect } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import * as api from '../lib/api';

export default function TopicsPage() {
  const [topics, setTopics] = useState([]);
  const [selectedSlug, setSelectedSlug] = useState(null);
  const [content, setContent] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  // Fetch topics list
  useEffect(() => {
    async function loadTopics() {
      try {
        const data = await api.listTopics();
        setTopics(data);
        if (data.length > 0 && !selectedSlug) {
          setSelectedSlug(data[0].slug);
        }
      } catch (e) {
        setError(e.message);
      } finally {
        setLoading(false);
      }
    }
    loadTopics();
  }, []);

  // Fetch selected topic content
  useEffect(() => {
    if (!selectedSlug) return;
    
    async function loadContent() {
      try {
        const data = await api.getTopic(selectedSlug);
        setContent(data.content);
      } catch (e) {
        setContent(null);
        setError(e.message);
      }
    }
    loadContent();
  }, [selectedSlug]);

  // Extract body content (after frontmatter)
  const getBodyContent = (md) => {
    if (!md) return '';
    const parts = md.split('---');
    if (parts.length >= 3) {
      return parts.slice(2).join('---').trim();
    }
    return md;
  };

  // Extract frontmatter as object
  const getFrontmatter = (md) => {
    if (!md) return {};
    const match = md.match(/^---\n([\s\S]*?)\n---/);
    if (!match) return {};
    
    const fm = {};
    match[1].split('\n').forEach(line => {
      const [key, ...valueParts] = line.split(':');
      if (key && valueParts.length) {
        fm[key.trim()] = valueParts.join(':').trim().replace(/^["']|["']$/g, '');
      }
    });
    return fm;
  };

  const frontmatter = getFrontmatter(content);

  return (
    <div style={{ height: 'calc(100vh - 48px)', display: 'flex', flexDirection: 'column' }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={{
          fontSize: '1.75rem',
          fontWeight: 700,
          letterSpacing: '-0.02em',
        }}>
          Topic Generation
        </h1>
        <p style={{
          color: 'var(--text-secondary)',
          fontSize: '0.9rem',
          marginTop: 4,
        }}>
          Browse incoming articles from _incoming directory
        </p>
      </div>

      {error && (
        <div style={{
          padding: '12px 16px',
          backgroundColor: 'rgba(239, 68, 68, 0.1)',
          border: '1px solid var(--accent-red)',
          borderRadius: 8,
          marginBottom: 20,
          color: 'var(--accent-red)',
          fontSize: '0.9rem',
        }}>
          {error}
        </div>
      )}

      <div style={{
        flex: 1,
        display: 'grid',
        gridTemplateColumns: '280px 1fr',
        gap: 20,
        minHeight: 0,
      }}>
        {/* Topic List */}
        <div style={{
          backgroundColor: 'var(--bg-card)',
          border: '1px solid var(--border-color)',
          borderRadius: 12,
          overflow: 'hidden',
          display: 'flex',
          flexDirection: 'column',
        }}>
          <div style={{
            padding: '12px 16px',
            borderBottom: '1px solid var(--border-color)',
            fontSize: '0.85rem',
            fontWeight: 600,
            color: 'var(--text-secondary)',
          }}>
            Articles ({topics.length})
          </div>
          
          <div style={{
            flex: 1,
            overflowY: 'auto',
          }}>
            {loading ? (
              <div style={{ padding: 16, color: 'var(--text-muted)' }}>
                Loading...
              </div>
            ) : topics.length === 0 ? (
              <div style={{ padding: 16, color: 'var(--text-muted)' }}>
                No articles found in _incoming
              </div>
            ) : (
              topics.map(topic => (
                <button
                  key={topic.slug}
                  onClick={() => setSelectedSlug(topic.slug)}
                  style={{
                    width: '100%',
                    padding: '12px 16px',
                    textAlign: 'left',
                    border: 'none',
                    backgroundColor: selectedSlug === topic.slug 
                      ? 'var(--bg-hover)' 
                      : 'transparent',
                    borderLeft: selectedSlug === topic.slug 
                      ? '3px solid var(--accent-blue)' 
                      : '3px solid transparent',
                    cursor: 'pointer',
                    transition: 'all 0.15s ease',
                  }}
                >
                  <div style={{
                    fontSize: '0.9rem',
                    fontWeight: selectedSlug === topic.slug ? 500 : 400,
                    color: 'var(--text-primary)',
                    marginBottom: 4,
                  }}>
                    {topic.title}
                  </div>
                  <div style={{
                    fontSize: '0.75rem',
                    color: 'var(--text-muted)',
                  }}>
                    {topic.domain} → {topic.category}
                  </div>
                </button>
              ))
            )}
          </div>
        </div>

        {/* Content Preview */}
        <div style={{
          backgroundColor: 'var(--bg-card)',
          border: '1px solid var(--border-color)',
          borderRadius: 12,
          overflow: 'hidden',
          display: 'flex',
          flexDirection: 'column',
        }}>
          {selectedSlug && content ? (
            <>
              {/* Frontmatter header */}
              <div style={{
                padding: '16px 20px',
                borderBottom: '1px solid var(--border-color)',
                backgroundColor: 'var(--bg-secondary)',
              }}>
                <h2 style={{
                  fontSize: '1.25rem',
                  fontWeight: 600,
                  marginBottom: 8,
                }}>
                  {frontmatter.article || selectedSlug}
                </h2>
                <div style={{
                  display: 'flex',
                  gap: 16,
                  flexWrap: 'wrap',
                  fontSize: '0.8rem',
                }}>
                  {frontmatter.domain && (
                    <span>
                      <span style={{ color: 'var(--text-muted)' }}>Domain: </span>
                      <span style={{ color: 'var(--accent-blue)' }}>{frontmatter.domain}</span>
                    </span>
                  )}
                  {frontmatter.category && (
                    <span>
                      <span style={{ color: 'var(--text-muted)' }}>Category: </span>
                      <span style={{ color: 'var(--accent-purple)' }}>{frontmatter.category}</span>
                    </span>
                  )}
                  {frontmatter.topic && (
                    <span>
                      <span style={{ color: 'var(--text-muted)' }}>Topic: </span>
                      <span style={{ color: 'var(--accent-green)' }}>{frontmatter.topic}</span>
                    </span>
                  )}
                  {frontmatter.model && (
                    <span>
                      <span style={{ color: 'var(--text-muted)' }}>Model: </span>
                      <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>{frontmatter.model}</span>
                    </span>
                  )}
                </div>
              </div>
              
              {/* Markdown content */}
              <div style={{
                flex: 1,
                overflowY: 'auto',
                padding: 20,
              }}>
                <article className="markdown-content">
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>
                    {getBodyContent(content)}
                  </ReactMarkdown>
                </article>
              </div>
            </>
          ) : (
            <div style={{
              flex: 1,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: 'var(--text-muted)',
            }}>
              {loading ? 'Loading...' : 'Select an article to preview'}
            </div>
          )}
        </div>
      </div>

      <style jsx global>{`
        .markdown-content {
          font-family: 'Inter', system-ui, sans-serif;
          line-height: 1.7;
          color: var(--text-primary);
        }
        .markdown-content h1, 
        .markdown-content h2, 
        .markdown-content h3, 
        .markdown-content h4 {
          margin-top: 1.5em;
          margin-bottom: 0.5em;
          font-weight: 600;
          line-height: 1.3;
        }
        .markdown-content h2 {
          font-size: 1.4rem;
          border-bottom: 1px solid var(--border-color);
          padding-bottom: 0.3em;
        }
        .markdown-content h3 {
          font-size: 1.15rem;
        }
        .markdown-content p {
          margin-bottom: 1em;
        }
        .markdown-content ul, .markdown-content ol {
          padding-left: 1.5em;
          margin-bottom: 1em;
        }
        .markdown-content li {
          margin-bottom: 0.3em;
        }
        .markdown-content code {
          font-family: 'JetBrains Mono', monospace;
          font-size: 0.85em;
          background: var(--bg-secondary);
          padding: 2px 6px;
          border-radius: 4px;
        }
        .markdown-content pre {
          background: var(--bg-secondary);
          padding: 16px;
          border-radius: 8px;
          overflow-x: auto;
          margin-bottom: 1em;
        }
        .markdown-content pre code {
          background: none;
          padding: 0;
        }
        .markdown-content blockquote {
          border-left: 3px solid var(--accent-blue);
          padding-left: 16px;
          margin-left: 0;
          color: var(--text-secondary);
          font-style: italic;
        }
        .markdown-content hr {
          border: none;
          border-top: 1px solid var(--border-color);
          margin: 2em 0;
        }
        .markdown-content img {
          max-width: 100%;
          border-radius: 8px;
        }
        .markdown-content table {
          width: 100%;
          border-collapse: collapse;
          margin-bottom: 1em;
        }
        .markdown-content th, .markdown-content td {
          border: 1px solid var(--border-color);
          padding: 8px 12px;
          text-align: left;
        }
        .markdown-content th {
          background: var(--bg-secondary);
          font-weight: 600;
        }
      `}</style>
    </div>
  );
}
