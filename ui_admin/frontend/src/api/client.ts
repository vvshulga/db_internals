const API_BASE = import.meta.env.VITE_API_BASE || 'http://localhost:8080/api';

export interface InfoResponse {
  database_dir: string;
  current_db: string;
  table_count: number;
  table_names: string[];
}

export interface TableSchema {
  name: string;
  columns: Column[];
}

export interface Column {
  name: string;
  type: string;
  nullable: boolean;
}

export interface RowData {
  rid: string;
  values: Record<string, any>;
}

export interface RowsResponse {
  rows: RowData[];
  total_count: number;
  page: number;
  page_size: number;
}

export interface QueryResponse {
  columns: string[];
  rows: any[][];
  row_count: number;
  execution_time_ms: number;
}

export class ApiClient {
  private async request<T>(path: string, options?: RequestInit): Promise<T> {
    const resp = await fetch(`${API_BASE}${path}`, {
      ...options,
      headers: { 'Content-Type': 'application/json', ...options?.headers },
    });

    if (!resp.ok) {
      const err = await resp.json().catch(() => ({ error: 'Request failed' }));
      throw new Error(err.error || 'Request failed');
    }

    return resp.json();
  }

  async getInfo(): Promise<InfoResponse> {
    return this.request('/info');
  }

  async listTables(): Promise<string[]> {
    const info = await this.getInfo();
    return info.table_names;
  }

  async getTableSchema(name: string): Promise<TableSchema> {
    return this.request(`/tables/${name}`);
  }

  async scanRows(table: string, page = 1, pageSize = 50): Promise<RowsResponse> {
    return this.request(`/tables/${table}/rows?page=${page}&page_size=${pageSize}`);
  }

  async insertRow(table: string, values: Record<string, any>): Promise<{ success: boolean; rid: string }> {
    return this.request(`/tables/${table}/rows`, {
      method: 'POST',
      body: JSON.stringify({ values }),
    });
  }

  async updateRow(table: string, rid: string, values: Record<string, any>): Promise<{ success: boolean; rid: string }> {
    return this.request(`/tables/${table}/rows/${rid}`, {
      method: 'PUT',
      body: JSON.stringify({ values }),
    });
  }

  async deleteRow(table: string, rid: string): Promise<{ success: boolean }> {
    return this.request(`/tables/${table}/rows/${rid}`, { method: 'DELETE' });
  }

  async createTable(name: string, columns: Column[]): Promise<{ success: boolean }> {
    return this.request('/tables', {
      method: 'POST',
      body: JSON.stringify({ name, columns }),
    });
  }

  async dropTable(name: string): Promise<{ success: boolean }> {
    return this.request(`/tables/${name}`, { method: 'DELETE' });
  }

  async executeQuery(sql: string): Promise<QueryResponse> {
    return this.request('/query', {
      method: 'POST',
      body: JSON.stringify({ sql }),
    });
  }

  async listDatabases(): Promise<string[]> {
    return this.request('/databases');
  }

  async switchDatabase(name: string): Promise<{ success: boolean; message: string }> {
    return this.request('/db/switch', {
      method: 'POST',
      body: JSON.stringify({ name }),
    });
  }
}

export const api = new ApiClient();
