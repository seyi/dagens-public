# Dagens Mission Control

The visual dashboard for the Dagens Distributed AI Agent Runtime.

## Tech Stack
- **Next.js 15 (App Router)**
- **Tailwind CSS**
- **Lucide Icons**
- **React Flow** (Coming soon for DAG visualization)

## Getting Started

1. **Install Dependencies:**
   ```bash
   npm install
   ```

2. **Run in Development Mode:**
   ```bash
   npm run dev
   ```

3. **Access the Dashboard:**
   Open [http://localhost:3000](http://localhost:3000)

## Configuration
The dashboard connects to the Dagens API (default: `http://localhost:8080`). 
Configure this in `.env.local`:
```bash
NEXT_PUBLIC_API_URL=http://localhost:8080
```