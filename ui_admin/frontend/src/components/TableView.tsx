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

  // Pagination state
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState(50);

  // Search state
  const [searchQuery, setSearchQuery] = useState('');
  const [searchColumn, setSearchColumn] = useState<string>('');

  useEffect(() => {
    loadData();
  }, [name]);

  const loadData = async () => {
    if (!name) return;

    setLoading(true);
    setError(null);

    try {
      const [schemaData, rowsData] = await Promise.all([
        api.getTableSchema(name),
        api.scanRows(name, 1, 1000), // Load more rows for client-side pagination
      ]);
      setSchema(schemaData);
      setRows(rowsData.rows);
      setCurrentPage(1);
      // Set default search column to first column
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

  // Filter rows based on search query
  const filteredRows = useMemo(() => {
    if (!searchQuery.trim() || !searchColumn) {
      return rows;
    }

    const query = searchQuery.toLowerCase();
    return rows.filter(row => {
      const value = row.values[searchColumn];
      if (value === null || value === undefined) {
        return 'null'.includes(query);
      }
      return String(value).toLowerCase().includes(query);
    });
  }, [rows, searchQuery, searchColumn]);

  // Paginate filtered rows
  const paginatedRows = useMemo(() => {
    const startIndex = (currentPage - 1) * pageSize;
    const endIndex = startIndex + pageSize;
    return filteredRows.slice(startIndex, endIndex);
  }, [filteredRows, currentPage, pageSize]);

  const totalPages = Math.ceil(filteredRows.length / pageSize);

  const handlePageChange = (page: number) => {
    if (page >= 1 && page <= totalPages) {
      setCurrentPage(page);
    }
  };

  const handlePageSizeChange = (newSize: number) => {
    setPageSize(newSize);
    setCurrentPage(1); // Reset to first page
  };

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
            placeholder="Search..."
            value={searchQuery}
            onChange={(e) => {
              setSearchQuery(e.target.value);
              setCurrentPage(1); // Reset to first page on search
            }}
            className="search-input"
          />
          {searchQuery && (
            <button
              className="secondary"
              onClick={() => {
                setSearchQuery('');
                setCurrentPage(1);
              }}
            >
              Clear
            </button>
          )}
        </div>

        <div className="action-controls">
          <button onClick={() => setShowInsert(true)}>Insert Row</button>
          <select
            value={pageSize}
            onChange={(e) => handlePageSizeChange(Number(e.target.value))}
            className="page-size-select"
          >
            <option value={10}>10 per page</option>
            <option value={25}>25 per page</option>
            <option value={50}>50 per page</option>
            <option value={100}>100 per page</option>
          </select>
        </div>
      </div>

      <div className="results-info">
        Showing {paginatedRows.length > 0 ? (currentPage - 1) * pageSize + 1 : 0} - {Math.min(currentPage * pageSize, filteredRows.length)} of {filteredRows.length} rows
        {searchQuery && ` (filtered from ${rows.length} total)`}
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
              {paginatedRows.map(row => (
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

          {totalPages > 1 && (
            <div className="pagination">
              <button
                onClick={() => handlePageChange(1)}
                disabled={currentPage === 1}
                className="secondary"
              >
                First
              </button>
              <button
                onClick={() => handlePageChange(currentPage - 1)}
                disabled={currentPage === 1}
                className="secondary"
              >
                Previous
              </button>

              <div className="page-numbers">
                {Array.from({ length: Math.min(5, totalPages) }, (_, i) => {
                  // Show pages around current page
                  let pageNum;
                  if (totalPages <= 5) {
                    pageNum = i + 1;
                  } else if (currentPage <= 3) {
                    pageNum = i + 1;
                  } else if (currentPage >= totalPages - 2) {
                    pageNum = totalPages - 4 + i;
                  } else {
                    pageNum = currentPage - 2 + i;
                  }

                  return (
                    <button
                      key={pageNum}
                      onClick={() => handlePageChange(pageNum)}
                      className={currentPage === pageNum ? 'active' : 'secondary'}
                    >
                      {pageNum}
                    </button>
                  );
                })}
              </div>

              <button
                onClick={() => handlePageChange(currentPage + 1)}
                disabled={currentPage === totalPages}
                className="secondary"
              >
                Next
              </button>
              <button
                onClick={() => handlePageChange(totalPages)}
                disabled={currentPage === totalPages}
                className="secondary"
              >
                Last
              </button>

              <span className="page-info">
                Page {currentPage} of {totalPages}
              </span>
            </div>
          )}
        </>
      ) : (
        <p>
          {searchQuery
            ? `No rows match your search "${searchQuery}" in column "${searchColumn}".`
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
