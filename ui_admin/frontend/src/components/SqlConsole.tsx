import { useState, useRef, useMemo } from 'react';
import { api, QueryResponse } from '../api/client';

const HISTORY_KEY = 'sql_console_history';
const MAX_HISTORY = 20;
const RESULT_PAGE_SIZE = 50;

interface SqlConsoleProps {
  onDBSwitch?: () => void; // called after a USE DATABASE succeeds so the navbar can refresh
}

export default function SqlConsole({ onDBSwitch }: SqlConsoleProps) {
  const [sql, setSql] = useState('');
  const [result, setResult] = useState<QueryResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [resultPage, setResultPage] = useState(1);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Query history — persisted in localStorage
  const [history, setHistory] = useState<string[]>(() => {
    try {
      return JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]');
    } catch {
      return [];
    }
  });
  const [showHistory, setShowHistory] = useState(false);

  const addToHistory = (query: string) => {
    const trimmed = query.trim();
    if (!trimmed) return;
    const updated = [trimmed, ...history.filter(q => q !== trimmed)].slice(0, MAX_HISTORY);
    setHistory(updated);
    localStorage.setItem(HISTORY_KEY, JSON.stringify(updated));
  };

  const clearHistory = () => {
    setHistory([]);
    localStorage.removeItem(HISTORY_KEY);
  };

  const execute = async () => {
    if (!sql.trim()) return;
    setLoading(true);
    setError(null);
    setResult(null);
    setResultPage(1);
    try {
      const r = await api.executeQuery(sql);
      setResult(r);
      addToHistory(sql);
      // If the query was a USE DATABASE statement, refresh the navbar db list.
      if (r.columns.length === 1 && r.columns[0] === 'message' && r.row_count === 1) {
        const msg = String(r.rows[0]?.[0] ?? '');
        if (msg.startsWith('Switched to database')) {
          onDBSwitch?.();
        }
      }
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      execute();
    }
  };

  const handleClear = () => {
    setSql('');
    setResult(null);
    setError(null);
    setResultPage(1);
    textareaRef.current?.focus();
  };

  const loadFromHistory = (query: string) => {
    setSql(query);
    setShowHistory(false);
    setTimeout(() => textareaRef.current?.focus(), 0);
  };

  // Paginate query results client-side (all rows are already in memory)
  const totalResultPages = result ? Math.ceil(result.rows.length / RESULT_PAGE_SIZE) : 0;
  const pagedRows = useMemo(() => {
    if (!result) return [];
    const start = (resultPage - 1) * RESULT_PAGE_SIZE;
    return result.rows.slice(start, start + RESULT_PAGE_SIZE);
  }, [result, resultPage]);

  const resultStart = result && result.rows.length > 0 ? (resultPage - 1) * RESULT_PAGE_SIZE + 1 : 0;
  const resultEnd = result ? Math.min(resultPage * RESULT_PAGE_SIZE, result.rows.length) : 0;

  return (
    <div className="sql-console">
      <h2>SQL Console</h2>

      <div className="sql-editor">
        <textarea
          ref={textareaRef}
          value={sql}
          onChange={e => setSql(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="SELECT * FROM users LIMIT 10;"
          rows={6}
          className="sql-textarea"
          disabled={loading}
          spellCheck={false}
        />
        <div className="sql-actions">
          <button onClick={execute} disabled={loading || !sql.trim()}>
            {loading ? 'Running…' : 'Execute'}
          </button>
          <button className="secondary" onClick={handleClear} disabled={loading}>
            Clear
          </button>
          <button
            className="secondary"
            onClick={() => setShowHistory(h => !h)}
            disabled={history.length === 0}
          >
            {showHistory ? 'Hide History' : `History (${history.length})`}
          </button>
          <span className="sql-hint">Ctrl+Enter to run</span>
        </div>
      </div>

      {showHistory && history.length > 0 && (
        <div className="history-panel">
          <div className="history-header">
            <span>Query History ({history.length})</span>
            <button className="secondary" onClick={clearHistory}>Clear</button>
          </div>
          <ul className="history-list">
            {history.map((q, i) => (
              <li key={i} onClick={() => loadFromHistory(q)} title={q}>
                <code>{q.length > 80 ? q.slice(0, 80) + '…' : q}</code>
              </li>
            ))}
          </ul>
        </div>
      )}

      {error && <div className="error">{error}</div>}

      {result && (
        <div className="query-results">
          <div className="results-info">
            {result.row_count} row(s) returned in {result.execution_time_ms}ms
            {result.row_count > RESULT_PAGE_SIZE && ` · showing ${resultStart}–${resultEnd}`}
          </div>

          {result.columns.length > 0 ? (
            <>
              <div className="results-table-wrap">
                <table>
                  <thead>
                    <tr>
                      {result.columns.map(c => <th key={c}>{c}</th>)}
                    </tr>
                  </thead>
                  <tbody>
                    {pagedRows.map((row, i) => (
                      <tr key={i}>
                        {row.map((cell, j) => (
                          <td key={j}>
                            {cell === null || cell === undefined
                              ? <em>NULL</em>
                              : String(cell)}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {totalResultPages > 1 && (
                <div className="pagination">
                  <button
                    onClick={() => setResultPage(1)}
                    disabled={resultPage === 1}
                    className="secondary"
                  >
                    First
                  </button>
                  <button
                    onClick={() => setResultPage(p => p - 1)}
                    disabled={resultPage === 1}
                    className="secondary"
                  >
                    Previous
                  </button>
                  <span className="page-info">
                    Page {resultPage} of {totalResultPages}
                  </span>
                  <button
                    onClick={() => setResultPage(p => p + 1)}
                    disabled={resultPage === totalResultPages}
                    className="secondary"
                  >
                    Next
                  </button>
                  <button
                    onClick={() => setResultPage(totalResultPages)}
                    disabled={resultPage === totalResultPages}
                    className="secondary"
                  >
                    Last
                  </button>
                </div>
              )}
            </>
          ) : (
            <p className="results-info">Query executed successfully.</p>
          )}
        </div>
      )}
    </div>
  );
}
