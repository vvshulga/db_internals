import { useEffect, useState, useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { api, TableSchema, RowData } from '../api/client';
import SchemaView from './SchemaView';
import RowEditor from './RowEditor';

export default function TableView() {
  const { name } = useParams<{ name: string }>();
  const [schema, setSchema] = useState<TableSchema | null>(null);
  const [rows, setRows] = useState<RowData[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editingRow, setEditingRow] = useState<RowData | null>(null);
  const [showInsert, setShowInsert] = useState(false);
  const [hasNextPage, setHasNextPage] = useState(false);

  // Pagination state
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState(50);

  // Search state (filters current page in-memory)
  const [searchQuery, setSearchQuery] = useState('');
  const [searchColumn, setSearchColumn] = useState<string>('');

  useEffect(() => {
    loadData();
  }, [name, currentPage, pageSize]);

  const loadData = async () => {
    if (!name) return;

    setLoading(true);
    setError(null);

    try {
      const [schemaData, rowsData] = await Promise.all([
        api.getTableSchema(name),
        api.scanRows(name, currentPage, pageSize),
      ]);
      setSchema(schemaData);
      setRows(rowsData.rows);
      setHasNextPage(rowsData.rows.length === pageSize);
      if (schemaData.columns.length > 0 && !searchColumn) {
        setSearchColumn(schemaData.columns[0].name);
      }
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleInsert = async (values: Record<string, any>) => {
    if (!name) return;
    await api.insertRow(name, values);
    setShowInsert(false);
    await loadData();
  };

  const handleUpdate = async (values: Record<string, any>) => {
    if (!name || !editingRow) return;
    await api.updateRow(name, editingRow.rid, values);
    setEditingRow(null);
    await loadData();
  };

  const handleDelete = async (rid: string) => {
    if (!name || !confirm('Delete this row?')) return;
    try {
      await api.deleteRow(name, rid);
      await loadData();
    } catch (err: any) {
      alert(`Failed to delete row: ${err.message}`);
    }
  };

  const handlePageSizeChange = (newSize: number) => {
    setPageSize(newSize);
    setCurrentPage(1);
  };

  // Filter current page rows by search query (client-side, within loaded page)
  const filteredRows = useMemo(() => {
    if (!searchQuery.trim() || !searchColumn) return rows;
    const q = searchQuery.toLowerCase();
    return rows.filter(row => {
      const value = row.values[searchColumn];
      if (value === null || value === undefined) return 'null'.includes(q);
      return String(value).toLowerCase().includes(q);
    });
  }, [rows, searchQuery, searchColumn]);

  const rowStart = (currentPage - 1) * pageSize + 1;
  const rowEnd = (currentPage - 1) * pageSize + rows.length;

  if (loading) return <div className="loading">Loading...</div>;
  if (error) return <div className="error">Error: {error}</div>;
  if (!schema) return <div className="error">Failed to load table schema</div>;

  return (
    <div className="table-view">
      <h2>Table: {name}</h2>
      <Link to="/tables">
        <button className="secondary">Back to Tables</button>
      </Link>

      <SchemaView schema={schema} />

      <h3>Rows</h3>

      <div className="table-controls">
        <div className="search-controls">
          <select
            value={searchColumn}
            onChange={(e) => setSearchColumn(e.target.value)}
            className="search-column-select"
          >
            {schema.columns.map(col => (
              <option key={col.name} value={col.name}>{col.name}</option>
            ))}
          </select>
          <input
            type="text"
            placeholder="Search current page..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="search-input"
          />
          {searchQuery && (
            <button className="secondary" onClick={() => setSearchQuery('')}>Clear</button>
          )}
        </div>

        <div className="action-controls">
          <button onClick={() => setShowInsert(true)}>Insert Row</button>
          <select
            value={pageSize}
            onChange={(e) => handlePageSizeChange(Number(e.target.value))}
            className="page-size-select"
          >
            <option value={25}>25 per page</option>
            <option value={50}>50 per page</option>
            <option value={100}>100 per page</option>
            <option value={250}>250 per page</option>
          </select>
        </div>
      </div>

      <div className="results-info">
        {rows.length === 0
          ? 'No rows'
          : `Rows ${rowStart}–${rowEnd}`}
        {searchQuery && ` · ${filteredRows.length} match${filteredRows.length !== 1 ? 'es' : ''} on this page`}
      </div>

      {filteredRows.length > 0 ? (
        <>
          <table>
            <thead>
              <tr>
                <th>RID</th>
                {schema.columns.map(col => <th key={col.name}>{col.name}</th>)}
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredRows.map(row => (
                <tr key={row.rid}>
                  <td><code>{row.rid}</code></td>
                  {schema.columns.map(col => (
                    <td key={col.name}>
                      {row.values[col.name] === null || row.values[col.name] === undefined
                        ? <em>NULL</em>
                        : String(row.values[col.name])}
                    </td>
                  ))}
                  <td>
                    <button onClick={() => setEditingRow(row)}>Edit</button>
                    <button className="danger" onClick={() => handleDelete(row.rid)}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <div className="pagination">
            <button
              onClick={() => setCurrentPage(1)}
              disabled={currentPage === 1}
              className="secondary"
            >
              First
            </button>
            <button
              onClick={() => setCurrentPage(p => p - 1)}
              disabled={currentPage === 1}
              className="secondary"
            >
              Previous
            </button>
            <span className="page-info">Page {currentPage}</span>
            <button
              onClick={() => setCurrentPage(p => p + 1)}
              disabled={!hasNextPage}
              className="secondary"
            >
              Next
            </button>
          </div>
        </>
      ) : (
        <p>
          {searchQuery
            ? `No rows on this page match "${searchQuery}" in column "${searchColumn}".`
            : 'No rows in this table. Insert a row above to get started.'}
        </p>
      )}

      {showInsert && (
        <RowEditor
          schema={schema}
          onSave={handleInsert}
          onCancel={() => setShowInsert(false)}
        />
      )}

      {editingRow && (
        <RowEditor
          schema={schema}
          initialValues={editingRow.values}
          onSave={handleUpdate}
          onCancel={() => setEditingRow(null)}
        />
      )}
    </div>
  );
}
