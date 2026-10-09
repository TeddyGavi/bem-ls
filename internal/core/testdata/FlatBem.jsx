// Flat BEM: elements are never chained, whatever the DOM depth.
export function Card({ title, children, featured }) {
  return (
    <article className={`card ${featured ? 'card--featured' : ''}`}>
      <header className="card__header">
        <h2 className="card__title">{title}</h2>
      </header>
      <div className="card__body">
        <p className="card__text">{children}</p>
        <footer className="card__footer">
          <a className="card__link card__link--primary" href="#">More</a>
        </footer>
      </div>
    </article>
  );
}
